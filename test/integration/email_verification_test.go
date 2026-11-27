package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/server"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/storage"

	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

const rateLimitWindowSeconds = 15 * 60

func (harness *harness) hitRateLimit(bucket string, key []byte) gen.HitRateLimitRow {
	harness.test.Helper()
	row, err := gen.New(harness.pool).HitRateLimit(context.Background(),
		gen.HitRateLimitParams{Bucket: bucket, Key: key, WindowSeconds: rateLimitWindowSeconds})
	if err != nil {
		harness.test.Fatalf("HitRateLimit: %v", err)
	}
	return row
}

func TestRateLimitCountsWithinTheWindow(test *testing.T) {
	harness := newHarness(test)
	digest := sha256.Sum256([]byte("192.0.2.1"))
	key := digest[:]

	for want := int32(1); want <= 31; want++ {
		row := harness.hitRateLimit("verify", key)
		if row.Hits != want {
			test.Fatalf("hit %d: hits = %d", want, row.Hits)
		}
		if want == 31 && (row.SecondsLeft <= 0 || row.SecondsLeft > rateLimitWindowSeconds) {
			test.Fatalf("seconds left = %v, want in (0, %d]", row.SecondsLeft, rateLimitWindowSeconds)
		}
	}
}

func TestRateLimitStartsANewWindowAfterFifteenMinutes(test *testing.T) {
	harness := newHarness(test)
	key := []byte("window-key")
	harness.hitRateLimit("verify", key)
	harness.hitRateLimit("verify", key)

	harness.execSQL("UPDATE auth_rate_limit SET window_started_at = now() - interval '14 minutes'")
	if row := harness.hitRateLimit("verify", key); row.Hits != 3 {
		test.Fatalf("hits inside the window = %d, want 3", row.Hits)
	}
	harness.execSQL("UPDATE auth_rate_limit SET window_started_at = now() - interval '15 minutes'")
	if row := harness.hitRateLimit("verify", key); row.Hits != 1 {
		test.Fatalf("hits after the window = %d, want 1", row.Hits)
	}
}

func TestRateLimitBucketsDoNotShareCounts(test *testing.T) {
	harness := newHarness(test)
	key := []byte("shared-key")
	harness.hitRateLimit("verify", key)
	harness.hitRateLimit("verify", key)
	if row := harness.hitRateLimit("resend", key); row.Hits != 1 {
		test.Fatalf("resend hits = %d, want 1", row.Hits)
	}
	if row := harness.hitRateLimit("verify", []byte("other-key")); row.Hits != 1 {
		test.Fatalf("other key hits = %d, want 1", row.Hits)
	}
}

func TestRateLimitCleanupDeletesRowsOlderThanADay(test *testing.T) {
	harness := newHarness(test)
	harness.hitRateLimit("verify", []byte("old"))
	harness.hitRateLimit("verify", []byte("recent"))
	harness.execSQL("UPDATE auth_rate_limit SET window_started_at = now() - interval '25 hours' WHERE key = 'old'")
	harness.execSQL("UPDATE auth_rate_limit SET window_started_at = now() - interval '23 hours' WHERE key = 'recent'")

	if err := gen.New(harness.pool).DeleteExpiredRateLimits(context.Background()); err != nil {
		test.Fatalf("DeleteExpiredRateLimits: %v", err)
	}
	var remaining [][]byte
	rows, err := harness.pool.Query(context.Background(), "SELECT key FROM auth_rate_limit")
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key); err != nil {
			test.Fatal(err)
		}
		remaining = append(remaining, key)
	}
	if len(remaining) != 1 || !bytes.Equal(remaining[0], []byte("recent")) {
		test.Fatalf("remaining keys = %q, want only recent", remaining)
	}
}

func TestRateLimitCleanupRetainsWindowsThatEndedLessThanADayAgo(test *testing.T) {
	harness := newHarness(test)
	key := []byte("recently-ended")
	harness.hitRateLimit("verify", key)
	harness.execSQL("UPDATE auth_rate_limit SET window_started_at = now() - interval '24 hours 5 minutes' WHERE key = $1", key)

	queries := gen.New(harness.pool)
	if err := queries.DeleteExpiredRateLimits(test.Context()); err != nil {
		test.Fatal(err)
	}
	var retained bool
	err := harness.pool.QueryRow(test.Context(), "SELECT EXISTS (SELECT 1 FROM auth_rate_limit WHERE key = $1)", key).Scan(&retained)
	if err != nil {
		test.Fatal(err)
	}
	if !retained {
		test.Fatal("cleanup deleted a window that ended less than one day ago")
	}
}

func TestRateLimitStoresTheDigestNotTheAddress(test *testing.T) {
	harness := newHarness(test)
	digest := sha256.Sum256([]byte("192.0.2.1"))
	harness.hitRateLimit("verify", digest[:])

	var stored []byte
	if err := harness.pool.QueryRow(context.Background(), "SELECT key FROM auth_rate_limit").Scan(&stored); err != nil {
		test.Fatal(err)
	}
	if len(stored) != 32 || bytes.Contains(stored, []byte("192.0.2.1")) {
		test.Fatalf("stored key = %x, want a 32-byte digest", stored)
	}
}

func TestSendModeRegistrationDoesNotClaimUnownedData(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	rollID := harness.addRoll(nil, harness.addStock(nil), 150000)
	harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": "claimant@example.com", "password": "Zone-System5"}), http.StatusCreated)

	var owned bool
	if err := harness.pool.QueryRow(context.Background(), "SELECT owner_id IS NOT NULL FROM roll WHERE id = $1", rollID).Scan(&owned); err != nil {
		test.Fatal(err)
	}
	if owned {
		test.Fatal("a password registration in send mode claimed an unowned roll")
	}
}

func TestIssuanceFailureRollsBackTheRegistration(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	harness.execSQL(`CREATE FUNCTION reject_challenge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.email_verification_selector IS NOT NULL THEN
			IF NEW.email = 'selector-conflict@example.com' THEN
				RAISE unique_violation USING CONSTRAINT = 'app_user_email_verification_selector';
			END IF;
			RAISE EXCEPTION 'challenge refused';
		END IF;
		RETURN NEW;
	END $$`)
	harness.execSQL("CREATE TRIGGER reject_challenge BEFORE UPDATE ON app_user FOR EACH ROW EXECUTE FUNCTION reject_challenge()")
	defer harness.execSQL("DROP TRIGGER reject_challenge ON app_user")
	mailBefore := len(harness.mail.Sent())

	for _, email := range []string{"failing@example.com", "selector-conflict@example.com"} {
		result := harness.browser().call("POST", "/auth/register", map[string]string{"email": email, "password": "Zone-System5"})
		var rows int
		if err := harness.pool.QueryRow(test.Context(), "SELECT count(*) FROM app_user WHERE email = $1", email).Scan(&rows); err != nil {
			test.Fatal(err)
		}
		if rows != 0 || len(harness.mail.Sent()) != mailBefore {
			test.Fatalf("%s: rows = %d, mail sent = %d, want none", email, rows, len(harness.mail.Sent())-mailBefore)
		}
		if result.Status != http.StatusInternalServerError || result.Body["code"] != "internal" {
			test.Errorf("%s: got %d %v, want 500 internal", email, result.Status, result.Body["code"])
		}
	}
}

func TestIssueEmailVerificationRules(test *testing.T) {
	harness := newHarness(test)
	queries := gen.New(harness.pool)
	issue := func(selectorByte byte) (string, error) {
		return queries.IssueEmailVerification(context.Background(), gen.IssueEmailVerificationParams{
			Email: actorEmail("tester"), Selector: bytes.Repeat([]byte{selectorByte}, 64), Verifier: bytes.Repeat([]byte{selectorByte}, 64),
		})
	}
	state := func() (selector byte, count int32) {
		var stored []byte
		if err := harness.pool.QueryRow(context.Background(),
			"SELECT email_verification_selector, email_verification_issued_count FROM app_user WHERE email = $1", actorEmail("tester")).Scan(&stored, &count); err != nil {
			test.Fatal(err)
		}
		return stored[0], count
	}
	reset := func(assignments string) {
		harness.execSQL("UPDATE app_user SET email_verified_at = NULL, email_verification_source = NULL, locked_until = NULL, " +
			"email_verification_last_issued_at = NULL, email_verification_window_started_at = NULL, email_verification_issued_count = 0")
		if assignments != "" {
			harness.execSQL("UPDATE app_user SET " + assignments)
		}
	}

	if got, err := issue(1); err != nil || got != actorEmail("tester") {
		test.Fatalf("first issue = %q, %v", got, err)
	}
	if _, err := issue(2); !db.IsNoRows(err) {
		test.Fatalf("second issue inside 60 seconds: err = %v, want no rows", err)
	}
	if selector, count := state(); selector != 1 || count != 1 {
		test.Fatalf("challenge changed: selector %d, count %d", selector, count)
	}

	reset("email_verification_window_started_at = now(), email_verification_issued_count = 5")
	if _, err := issue(3); !db.IsNoRows(err) {
		test.Fatalf("issue with 5 in the window: err = %v, want no rows", err)
	}
	reset("email_verification_window_started_at = now() - interval '25 hours', email_verification_issued_count = 5, email_verification_last_issued_at = now() - interval '25 hours'")
	if _, err := issue(4); err != nil {
		test.Fatalf("issue after the window: %v", err)
	}
	if selector, count := state(); selector != 4 || count != 1 {
		test.Fatalf("after window reset: selector %d, count %d, want 4 and 1", selector, count)
	}

	reset("email_verified_at = now(), email_verification_source = 'link'")
	if _, err := issue(5); !db.IsNoRows(err) {
		test.Fatalf("issue for a verified row: err = %v, want no rows", err)
	}
	reset("locked_until = now() + interval '10 minutes'")
	if _, err := issue(6); !db.IsNoRows(err) {
		test.Fatalf("issue for a locked row: err = %v, want no rows", err)
	}
}

const verifyTestPassword = "Zone-System5"

// registerForToken registers an account in send mode and returns the token from the mailed link.
func (harness *harness) registerForToken(email string) string {
	harness.test.Helper()
	harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": email, "password": verifyTestPassword}), http.StatusCreated)
	sent := harness.mail.Sent()
	for _, word := range strings.Fields(sent[len(sent)-1].TextBody) {
		if token, found := strings.CutPrefix(word, "http://api.test/auth/verify-email#token="); found {
			return token
		}
	}
	harness.test.Fatal("no verification link in the mail")
	return ""
}

// postVerify sends POST /auth/verify-email and returns the status, the problem code and Retry-After.
func (harness *harness) postVerify(token, password string) (status int, code, retryAfter string) {
	body := map[string]string{"token": token}
	if password != "" {
		body["password"] = password
	}
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", "/auth/verify-email", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	harness.app.ServeHTTP(recorder, request)
	var decoded struct{ Code string }
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded.Code, recorder.Header().Get("Retry-After")
}

func (harness *harness) verified(email string) (proof *string, challengeKept bool) {
	harness.test.Helper()
	err := harness.pool.QueryRow(context.Background(),
		"SELECT email_verification_source, email_verification_selector IS NOT NULL FROM app_user WHERE email = $1", email).Scan(&proof, &challengeKept)
	if err != nil {
		harness.test.Fatal(err)
	}
	return proof, challengeKept
}

func TestVerifyEmailRefusesAnExpiredToken(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	token := harness.registerForToken("expired@example.com")
	harness.execSQL("UPDATE app_user SET email_verification_expires_at = now() WHERE email = 'expired@example.com'")
	if status, code, _ := harness.postVerify(token, verifyTestPassword); status != http.StatusUnprocessableEntity || code != "invalid_token" {
		test.Fatalf("expired token = %d %q, want 422 invalid_token", status, code)
	}
	if proof, kept := harness.verified("expired@example.com"); proof != nil || !kept {
		test.Fatalf("proof = %v, challenge kept = %v", proof, kept)
	}
}

func TestVerifyEmailRefusesAPasswordThatAResetReplaced(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	token := harness.registerForToken("reset@example.com")
	replaced, err := authboss.NewBCryptHasher(4).GenerateHash("Fresh-Neg4tive")
	if err != nil {
		test.Fatal(err)
	}
	harness.execSQL("UPDATE app_user SET password_hash = $1 WHERE email = 'reset@example.com'", replaced)
	if status, code, _ := harness.postVerify(token, verifyTestPassword); status != http.StatusUnprocessableEntity || code != "invalid_token" {
		test.Fatalf("old password = %d %q, want 422 invalid_token", status, code)
	}
	if proof, kept := harness.verified("reset@example.com"); proof != nil || !kept {
		test.Fatalf("proof = %v, challenge kept = %v", proof, kept)
	}
}

// The handler loads the row and passes what it checked. The statement must still refuse when the
// row changes between that read and the write, which a request cannot time.
func TestConsumeEmailVerificationRefusesAChangedRow(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	harness.registerForToken("race@example.com")
	queries := gen.New(harness.pool)
	row, err := queries.GetUserByEmail(context.Background(), "race@example.com")
	if err != nil {
		test.Fatal(err)
	}
	consume := func(params gen.ConsumeEmailVerificationParams) error {
		_, err := queries.ConsumeEmailVerification(context.Background(), params)
		return err
	}
	good := gen.ConsumeEmailVerificationParams{
		ID: row.ID, Selector: row.EmailVerificationSelector, Verifier: row.EmailVerificationVerifier,
		PasswordHash: row.PasswordHash, Email: row.Email,
	}
	otherHash := *row.PasswordHash + "x"
	staleHash, staleEmail := good, good
	staleHash.PasswordHash = &otherHash
	staleEmail.Email = "moved@example.com"
	for name, params := range map[string]gen.ConsumeEmailVerificationParams{"password hash": staleHash, "email": staleEmail} {
		if err := consume(params); !db.IsNoRows(err) {
			test.Fatalf("stale %s: error = %v, want no rows", name, err)
		}
	}
	if proof, kept := harness.verified("race@example.com"); proof != nil || !kept {
		test.Fatalf("a refused write changed the row: proof = %v, challenge kept = %v", proof, kept)
	}
	if err := consume(good); err != nil {
		test.Fatalf("matching row: %v", err)
	}
	if err := consume(good); !db.IsNoRows(err) {
		test.Fatalf("second write: error = %v, want no rows", err)
	}
}

func TestVerifyEmailClaimsUnownedDataOnceAcrossConcurrentRequests(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	rollID := harness.addRoll(nil, harness.addStock(nil), nil)
	token := harness.registerForToken("claimant@example.com")
	otherToken := harness.registerForToken("later@example.com")
	owner := func() *string {
		var ownerID *string
		if err := harness.pool.QueryRow(context.Background(), "SELECT owner_id::text FROM roll WHERE id = $1", rollID).Scan(&ownerID); err != nil {
			test.Fatal(err)
		}
		return ownerID
	}
	if owner() != nil {
		test.Fatal("registration claimed the roll before verification")
	}

	const attempts = 6
	statuses := make([]int, attempts)
	var group sync.WaitGroup
	for index := range statuses {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses[index], _, _ = harness.postVerify(token, verifyTestPassword)
		}()
	}
	group.Wait()
	counts := map[int]int{}
	for _, status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusUnprocessableEntity] != attempts-1 {
		test.Fatalf("statuses = %v, want one 200 and %d 422", statuses, attempts-1)
	}
	var claimantID string
	if err := harness.pool.QueryRow(context.Background(), "SELECT id::text FROM app_user WHERE email = 'claimant@example.com'").Scan(&claimantID); err != nil {
		test.Fatal(err)
	}
	if ownerID := owner(); ownerID == nil || *ownerID != claimantID {
		test.Fatalf("roll owner = %v, want %s", ownerID, claimantID)
	}
	if proof, kept := harness.verified("claimant@example.com"); proof == nil || *proof != "link" || kept {
		test.Fatalf("proof = %v, challenge kept = %v", proof, kept)
	}

	// A later verification takes nothing that has an owner.
	if status, _, _ := harness.postVerify(otherToken, verifyTestPassword); status != http.StatusOK {
		test.Fatalf("second account verify = %d, want 200", status)
	}
	if ownerID := owner(); ownerID == nil || *ownerID != claimantID {
		test.Fatalf("roll owner after the second verify = %v, want %s", ownerID, claimantID)
	}
}

func TestVerifyEmailRollsBackProofWhenTheClaimFails(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	const email = "claim-failure@example.com"
	rollID := harness.addRoll(nil, harness.addStock(nil), nil)
	token := harness.registerForToken(email)
	harness.execSQL(`CREATE FUNCTION reject_verification_claim() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'claim refused'; END $$`)
	harness.execSQL(`CREATE TRIGGER reject_verification_claim BEFORE UPDATE OF owner_id ON roll
	FOR EACH ROW WHEN (OLD.owner_id IS NULL AND NEW.owner_id IS NOT NULL)
	EXECUTE FUNCTION reject_verification_claim()`)

	if status, code, _ := harness.postVerify(token, verifyTestPassword); status != http.StatusInternalServerError || code != "internal" {
		test.Fatalf("failed claim = %d %q, want 500 internal", status, code)
	}
	if proof, kept := harness.verified(email); proof != nil || !kept {
		test.Fatalf("proof = %v, challenge kept = %v, want no proof and retained challenge", proof, kept)
	}
	var owned bool
	if err := harness.pool.QueryRow(test.Context(), "SELECT owner_id IS NOT NULL FROM roll WHERE id = $1", rollID).Scan(&owned); err != nil {
		test.Fatal(err)
	}
	if owned {
		test.Fatal("failed claim assigned an owner to the roll")
	}
}

func TestVerifyEmailLimitsOneSource(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	raw := make([]byte, 64)
	_, _ = rand.Read(raw)
	unknown := base64.URLEncoding.EncodeToString(raw)
	for attempt := 1; attempt <= 30; attempt++ {
		if status, _, _ := harness.postVerify(unknown, ""); status != http.StatusUnprocessableEntity {
			test.Fatalf("request %d = %d, want 422", attempt, status)
		}
	}
	status, code, retryAfter := harness.postVerify(unknown, "")
	if seconds, err := strconv.Atoi(retryAfter); status != http.StatusTooManyRequests || code != "rate_limited" || err != nil || seconds < 1 {
		test.Fatalf("31st request = %d %q Retry-After %q, want 429 rate_limited with positive seconds", status, code, retryAfter)
	}
}

// The write waits for the row lock. The challenge expires during the wait, so the check must use the
// time after the lock, not the time at the start of the statement.
func TestVerifyEmailRefusesAChallengeThatExpiresWhileWaitingForTheRowLock(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	token := harness.registerForToken("waiting@example.com")
	ctx := context.Background()
	holder, err := harness.pool.Begin(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT 1 FROM app_user WHERE email = 'waiting@example.com' FOR UPDATE"); err != nil {
		test.Fatal(err)
	}

	answer := make(chan int, 1)
	go func() {
		status, _, _ := harness.postVerify(token, verifyTestPassword)
		answer <- status
	}()
	time.Sleep(700 * time.Millisecond) // the request has passed its checks and waits on the lock
	if _, err := holder.Exec(ctx, "UPDATE app_user SET email_verification_expires_at = clock_timestamp() + interval '1 second' WHERE email = 'waiting@example.com'"); err != nil {
		test.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if err := holder.Commit(ctx); err != nil {
		test.Fatal(err)
	}
	if status := <-answer; status != http.StatusUnprocessableEntity {
		test.Fatalf("status = %d, want 422", status)
	}
	if proof, kept := harness.verified("waiting@example.com"); proof != nil || !kept {
		test.Fatalf("proof = %v, challenge kept = %v", proof, kept)
	}
}

// A holder releases the row without changing it, so PostgreSQL does not re-check the update after the wait.
// The handler must take the row lock first and check the expiry after it.
func TestVerifyEmailRefusesAChallengeThatExpiresWhileTheLockHolderChangesNothing(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	token := harness.registerForToken("unchanged@example.com")
	ctx := context.Background()
	harness.execSQL("UPDATE app_user SET email_verification_expires_at = clock_timestamp() + interval '1 second' WHERE email = 'unchanged@example.com'")
	holder, err := harness.pool.Begin(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT 1 FROM app_user WHERE email = 'unchanged@example.com' FOR UPDATE"); err != nil {
		test.Fatal(err)
	}

	answer := make(chan int, 1)
	go func() {
		status, _, _ := harness.postVerify(token, verifyTestPassword)
		answer <- status
	}()
	// Wait until a backend waits for the row lock, then until the challenge has expired.
	waitFor := func(what, query string) {
		test.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var ready bool
			if err := harness.pool.QueryRow(ctx, query).Scan(&ready); err != nil {
				test.Fatal(err)
			}
			if ready {
				return
			}
			if time.Now().After(deadline) {
				test.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor("a backend waiting for the row lock", `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE NOT granted AND locktype = 'transactionid')`)
	waitFor("the challenge to expire", "SELECT clock_timestamp() > email_verification_expires_at FROM app_user WHERE email = 'unchanged@example.com'")
	if err := holder.Rollback(ctx); err != nil {
		test.Fatal(err)
	}
	if status := <-answer; status != http.StatusUnprocessableEntity {
		test.Fatalf("status = %d, want 422", status)
	}
	if proof, kept := harness.verified("unchanged@example.com"); proof != nil || !kept {
		test.Fatalf("proof = %v, challenge kept = %v", proof, kept)
	}
}

// postResend sends POST /auth/verify-email/resend and returns the status.
func (harness *harness) postResend(address string) int {
	payload, _ := json.Marshal(map[string]string{"email": address})
	request := httptest.NewRequest("POST", "/auth/verify-email/resend", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	harness.app.ServeHTTP(recorder, request)
	return recorder.Code
}

func (harness *harness) issuedCount(email string) (count int, challengeKept bool) {
	harness.test.Helper()
	err := harness.pool.QueryRow(context.Background(),
		"SELECT email_verification_issued_count, email_verification_selector IS NOT NULL FROM app_user WHERE email = $1", email).Scan(&count, &challengeKept)
	if err != nil {
		harness.test.Fatal(err)
	}
	return count, challengeKept
}

func TestResendRotatesTheChallengeOnceAcrossConcurrentRequests(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	oldToken := harness.registerForToken("resend@example.com")
	harness.execSQL("UPDATE app_user SET email_verification_last_issued_at = now() - interval '2 minutes' WHERE email = 'resend@example.com'")
	mailBefore := len(harness.mail.Sent())

	var group sync.WaitGroup
	statuses := make([]int, 8)
	for index := range statuses {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses[index] = harness.postResend("resend@example.com")
		}()
	}
	group.Wait()

	for _, status := range statuses {
		if status != http.StatusAccepted {
			test.Fatalf("statuses = %v, want all 202", statuses)
		}
	}
	if count, _ := harness.issuedCount("resend@example.com"); count != 2 || len(harness.mail.Sent()) != mailBefore+1 {
		test.Fatalf("issued count = %d, mail %d (was %d), want 2 issues and one new mail", count, len(harness.mail.Sent()), mailBefore)
	}
	if status, code, _ := harness.postVerify(oldToken, verifyTestPassword); status != http.StatusUnprocessableEntity || code != "invalid_token" {
		test.Fatalf("old token = %d %q, want 422 invalid_token", status, code)
	}
}

func TestResendAfterVerifyCommitsIssuesNothing(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	token := harness.registerForToken("late@example.com")
	if status, _, _ := harness.postVerify(token, verifyTestPassword); status != http.StatusOK {
		test.Fatalf("verify = %d, want 200", status)
	}
	harness.execSQL("UPDATE app_user SET email_verification_last_issued_at = now() - interval '2 minutes' WHERE email = 'late@example.com'")
	mailBefore := len(harness.mail.Sent())
	if status := harness.postResend("late@example.com"); status != http.StatusAccepted {
		test.Fatalf("resend = %d, want 202", status)
	}
	if count, kept := harness.issuedCount("late@example.com"); kept || count != 1 || len(harness.mail.Sent()) != mailBefore {
		test.Fatalf("resend after proof: issued count %d, challenge kept %v, mail %d (was %d)", count, kept, len(harness.mail.Sent()), mailBefore)
	}
}

// verificationTokenFromLastMail returns the token in the last mailed verification link.
func (harness *harness) verificationTokenFromLastMail() string {
	harness.test.Helper()
	sent := harness.mail.Sent()
	for _, word := range strings.Fields(sent[len(sent)-1].TextBody) {
		if token, found := strings.CutPrefix(word, "http://api.test/auth/verify-email#token="); found {
			return token
		}
	}
	harness.test.Fatal("no verification link in the mail")
	return ""
}

func TestRecoveryClearsTheChallengeAndAResendVerifiesWithTheNewPassword(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	oldToken := harness.registerForToken("recover@example.com")
	browser := harness.browser()
	harness.expect(browser.call("POST", "/auth/recover", map[string]string{"email": "recover@example.com"}), http.StatusOK)
	newPassword := "Fresh-Neg4tive"
	harness.expect(browser.call("POST", "/auth/recover/end", map[string]string{"token": harness.mail.RecoverToken(), "password": newPassword}), http.StatusOK)

	if _, kept := harness.verified("recover@example.com"); kept {
		test.Fatal("a reset must clear the open challenge")
	}
	if status, code, _ := harness.postVerify(oldToken, newPassword); status != http.StatusUnprocessableEntity || code != "invalid_token" {
		test.Fatalf("token from before the reset = %d %q, want 422 invalid_token", status, code)
	}
	harness.execSQL("UPDATE app_user SET email_verification_last_issued_at = now() - interval '2 minutes' WHERE email = 'recover@example.com'")
	if status := harness.postResend("recover@example.com"); status != http.StatusAccepted {
		test.Fatalf("resend = %d, want 202", status)
	}
	if status, _, _ := harness.postVerify(harness.verificationTokenFromLastMail(), newPassword); status != http.StatusOK {
		test.Fatalf("verify with the new password = %d, want 200", status)
	}
}

func TestLoginKeepsTheChallengeAndFailedLoginsToo(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	harness.registerForToken("keep@example.com")
	browser := harness.browser()
	harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "keep@example.com", "password": "Wrong-Pass1"}), http.StatusUnauthorized)
	if _, kept := harness.verified("keep@example.com"); !kept {
		test.Fatal("a failed login must keep the challenge")
	}
	harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "keep@example.com", "password": verifyTestPassword}), http.StatusOK)
	if _, kept := harness.verified("keep@example.com"); !kept {
		test.Fatal("a login must keep the challenge")
	}
}

func TestInsertSessionForPasswordRefusesAChangedHash(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	harness.registerForToken("session@example.com")
	queries := gen.New(harness.pool)
	row, err := queries.GetUserByEmail(context.Background(), "session@example.com")
	if err != nil {
		test.Fatal(err)
	}
	insert := func(token string, hash *string) int64 {
		inserted, err := queries.InsertSessionForPassword(context.Background(), gen.InsertSessionForPasswordParams{
			TokenHash: []byte(token), UserID: row.ID, PasswordHash: hash,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		})
		if err != nil {
			test.Fatal(err)
		}
		return inserted
	}
	changed := *row.PasswordHash + "x"
	if inserted := insert("stale", &changed); inserted != 0 {
		test.Fatalf("a changed hash inserted %d rows, want 0", inserted)
	}
	if inserted := insert("current", row.PasswordHash); inserted != 1 {
		test.Fatalf("the current hash inserted %d rows, want 1", inserted)
	}
}

// A reset that holds the account row makes a login wait. The login must then see the new hash and
// start no session.
func TestLoginWaitsForAResetThatHoldsTheRowAndStartsNoSession(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend)
	harness.registerForToken("waits@example.com")
	ctx := context.Background()
	countSessions := func() (count int) {
		if err := harness.pool.QueryRow(ctx, "SELECT count(*) FROM user_session").Scan(&count); err != nil {
			test.Fatal(err)
		}
		return count
	}
	sessionsBefore := countSessions()
	holder, err := harness.pool.Begin(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT 1 FROM app_user WHERE email = 'waits@example.com' FOR UPDATE"); err != nil {
		test.Fatal(err)
	}

	browser := harness.browser()
	answer := make(chan int, 1)
	go func() {
		answer <- browser.call("POST", "/auth/login", map[string]string{"email": "waits@example.com", "password": verifyTestPassword}).Status
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := harness.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE NOT granted AND locktype = 'transactionid')`).Scan(&waiting); err != nil {
			test.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("timed out waiting for the login to wait for the row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := holder.Exec(ctx, "UPDATE app_user SET password_hash = password_hash || 'x' WHERE email = 'waits@example.com'"); err != nil {
		test.Fatal(err)
	}
	if err := holder.Commit(ctx); err != nil {
		test.Fatal(err)
	}
	<-answer

	if count := countSessions(); count != sessionsBefore {
		test.Fatalf("sessions = %d (was %d), want no new session", count, sessionsBefore)
	}
	serverURL, _ := url.Parse(browser.server.URL)
	for _, cookie := range browser.client.Jar.Cookies(serverURL) {
		if cookie.Name == auth.SessionCookie && cookie.Value != "" {
			test.Fatal("the login must not set a session cookie")
		}
	}
}

// enforceHandler serves the harness database with EMAIL_VERIFICATION=enforce.
func (harness *harness) enforceHandler() http.Handler {
	harness.test.Helper()
	store := db.NewPostgresStore(harness.pool)
	accounts, err := auth.New(store, auth.Config{EmailVerification: auth.EmailVerificationEnforce, AppURL: "http://app.test", APIURL: "http://api.test", Mailer: &harness.mail})
	if err != nil {
		harness.test.Fatal(err)
	}
	files, err := storage.NewLocal(harness.test.TempDir())
	if err != nil {
		harness.test.Fatal(err)
	}
	handler, err := server.NewHandler(controllers.New(services.New(store, files, clock.System{})), accounts, []string{"*"})
	if err != nil {
		harness.test.Fatal(err)
	}
	return handler
}

func TestEnforceTurnsAnUnverifiedSessionAnonymousAndWritesNothingUntilProof(test *testing.T) {
	harness := newHarnessWithVerification(test, auth.EmailVerificationSend) // the "tester" account is unverified
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	edit := func(description string) map[string]any {
		return map[string]any{"filmStockId": seeded.stockID, "format": 135, "exposures": 36, "description": description}
	}
	login := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"`+actorEmail("tester")+`","password":"`+verifyTestPassword+`"}`))
	login.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	harness.app.ServeHTTP(recorder, login)
	var cookie *http.Cookie
	for _, candidate := range recorder.Result().Cookies() {
		if candidate.Name == auth.SessionCookie {
			cookie = candidate
		}
	}
	if recorder.Code != http.StatusOK || cookie == nil {
		test.Fatalf("login in send mode = %d, cookie %v", recorder.Code, cookie)
	}

	enforced := testutil.SignedIn(harness.enforceHandler(), cookie)
	send := func(method, path string, body any) int {
		payload, _ := json.Marshal(body)
		request := httptest.NewRequest(method, path, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		enforced.ServeHTTP(result, request)
		return result.Code
	}
	if status := send("GET", "/auth/me", nil); status != http.StatusUnauthorized {
		test.Fatalf("GET /auth/me under enforce = %d, want 401", status)
	}
	if status := send("PUT", "/rolls/"+rollID, edit("blocked edit")); status != http.StatusUnauthorized {
		test.Fatalf("PUT /rolls under enforce = %d, want 401", status)
	}
	if roll := harness.rollOf(rollID); roll["description"] == "blocked edit" {
		test.Fatalf("a refused write changed the roll: %v", roll)
	}

	if status, _, _ := harness.postVerify(harness.verificationTokenFromLastMail(), verifyTestPassword); status != http.StatusOK {
		test.Fatalf("verify = %d, want 200", status)
	}
	if status := send("GET", "/auth/me", nil); status != http.StatusOK {
		test.Fatalf("GET /auth/me after proof = %d, want 200 with the same cookie", status)
	}
}
