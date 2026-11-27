package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

// insertVerificationRow inserts one account with extra column assignments and returns the database error.
func (harness *harness) insertVerificationRow(extra string, arguments ...any) error {
	harness.test.Helper()
	statement := "INSERT INTO app_user (id, email, password_hash, " + extra + ") VALUES ($1, $2, 'unused', " +
		placeholders(len(arguments)) + ")"
	_, err := harness.pool.Exec(context.Background(), statement, append([]any{newID(), newID() + "@example.test"}, arguments...)...)
	return err
}

// placeholders returns "$3, $4, ..." for count values after the id and the email.
func placeholders(count int) string {
	parts := make([]string, count)
	for index := range parts {
		parts[index] = "$" + string(rune('3'+index))
	}
	return strings.Join(parts, ", ")
}

func sqlState(test *testing.T, err error) string {
	test.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) {
		test.Fatalf("error = %v, want a database error", err)
	}
	return pgError.Code
}

func TestEmailVerificationMigrationKeepsExistingRows(test *testing.T) {
	harness := newHarness(test)
	test.Cleanup(func() { goose.SetBaseFS(nil) })

	harness.migrateTo(21, true)
	passwordID, googleID, bothID := newID(), newID(), newID()
	harness.execSQL("INSERT INTO app_user (id, email, password_hash) VALUES ($1, 'password@example.test', 'hash-password')", passwordID)
	harness.execSQL("INSERT INTO app_user (id, email, oauth2_provider, oauth2_uid) VALUES ($1, 'google@example.test', 'google', 'sub-google')", googleID)
	harness.execSQL("INSERT INTO app_user (id, email, password_hash, oauth2_provider, oauth2_uid) VALUES ($1, 'both@example.test', 'hash-both', 'google', 'sub-both')", bothID)

	harness.migrateTo(22, false)

	for _, want := range []struct {
		id, email, hash, uid string
	}{
		{passwordID, "password@example.test", "hash-password", ""},
		{googleID, "google@example.test", "", "sub-google"},
		{bothID, "both@example.test", "hash-both", "sub-both"},
	} {
		var email, hash, uid string
		var proofNull, sourceNull, challengeNull, exempt bool
		var count int
		err := harness.pool.QueryRow(context.Background(), `SELECT email, coalesce(password_hash, ''), coalesce(oauth2_uid, ''),
				email_verified_at IS NULL, email_verification_source IS NULL,
				email_verification_selector IS NULL AND email_verification_verifier IS NULL AND email_verification_expires_at IS NULL
				AND email_verification_last_issued_at IS NULL AND email_verification_window_started_at IS NULL,
				email_verification_exempt, email_verification_issued_count
			FROM app_user WHERE id = $1`, want.id).Scan(&email, &hash, &uid, &proofNull, &sourceNull, &challengeNull, &exempt, &count)
		if err != nil {
			test.Fatal(err)
		}
		if email != want.email || hash != want.hash || uid != want.uid {
			test.Errorf("row %s = %q %q %q, want %q %q %q", want.id, email, hash, uid, want.email, want.hash, want.uid)
		}
		if !proofNull || !sourceNull || !challengeNull || exempt || count != 0 {
			test.Errorf("row %s: proof null %v, source null %v, challenge null %v, exempt %v, count %d; want null, null, null, false, 0",
				want.id, proofNull, sourceNull, challengeNull, exempt, count)
		}
	}
}

func TestEmailVerificationConstraintsRejectBadRows(test *testing.T) {
	harness := newHarness(test)
	selector, verifier := strings.Repeat("s", 64), strings.Repeat("v", 64)
	const checkViolation = "23514"

	for name, run := range map[string]func() error{
		"source without time": func() error {
			return harness.insertVerificationRow("email_verification_source", "link")
		},
		"time without source": func() error {
			return harness.insertVerificationRow("email_verified_at", "2026-01-01T00:00:00Z")
		},
		"unknown source": func() error {
			return harness.insertVerificationRow("email_verified_at, email_verification_source", "2026-01-01T00:00:00Z", "other")
		},
		"proof with exemption": func() error {
			return harness.insertVerificationRow("email_verified_at, email_verification_source, email_verification_exempt",
				"2026-01-01T00:00:00Z", "link", true)
		},
		"selector without verifier": func() error {
			return harness.insertVerificationRow("email_verification_selector, email_verification_expires_at", []byte(selector), "2026-01-01T00:00:00Z")
		},
		"selector without expiry": func() error {
			return harness.insertVerificationRow("email_verification_selector, email_verification_verifier", []byte(selector), []byte(verifier))
		},
		"selector of 63 bytes": func() error {
			return harness.insertVerificationRow("email_verification_selector, email_verification_verifier, email_verification_expires_at",
				[]byte(selector[:63]), []byte(verifier), "2026-01-01T00:00:00Z")
		},
		"verifier of 63 bytes": func() error {
			return harness.insertVerificationRow("email_verification_selector, email_verification_verifier, email_verification_expires_at",
				[]byte(selector), []byte(verifier[:63]), "2026-01-01T00:00:00Z")
		},
		"negative issued count": func() error {
			return harness.insertVerificationRow("email_verification_issued_count", -1)
		},
	} {
		test.Run(name, func(test *testing.T) {
			if code := sqlState(test, run()); code != checkViolation {
				test.Errorf("SQLSTATE = %s, want %s", code, checkViolation)
			}
		})
	}

	// A full challenge and a full proof are valid.
	if err := harness.insertVerificationRow("email_verification_selector, email_verification_verifier, email_verification_expires_at",
		[]byte(selector), []byte(verifier), "2026-01-01T00:00:00Z"); err != nil {
		test.Errorf("valid challenge rejected: %v", err)
	}
	if err := harness.insertVerificationRow("email_verified_at, email_verification_source", "2026-01-01T00:00:00Z", "google"); err != nil {
		test.Errorf("valid proof rejected: %v", err)
	}
}

func TestEmailVerificationSelectorIsUnique(test *testing.T) {
	harness := newHarness(test)
	columns := "email_verification_selector, email_verification_verifier, email_verification_expires_at"
	selector := []byte(strings.Repeat("s", 64))

	if err := harness.insertVerificationRow(columns, selector, []byte(strings.Repeat("a", 64)), "2026-01-01T00:00:00Z"); err != nil {
		test.Fatal(err)
	}
	err := harness.insertVerificationRow(columns, selector, []byte(strings.Repeat("b", 64)), "2026-01-01T00:00:00Z")
	if code := sqlState(test, err); code != "23505" {
		test.Errorf("duplicate selector SQLSTATE = %s, want 23505", code)
	}
	// Many rows without a selector are fine.
	for range 3 {
		if err := harness.insertVerificationRow("email_verification_issued_count", 0); err != nil {
			test.Errorf("row without selector rejected: %v", err)
		}
	}
}
