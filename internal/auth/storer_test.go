package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

func googleDetails(uid, email string) map[string]string {
	return map[string]string{"uid": uid, "email": email, "email_verified": "true", "name": "Ansel"}
}

func TestLoadPreservesEmailVerificationState(test *testing.T) {
	verifiedAt := time.Date(2026, 11, 26, 1, 2, 3, 0, time.UTC)
	for _, scenario := range []struct {
		name       string
		verifiedAt time.Time
		exempt     bool
	}{
		{name: "unverified"},
		{name: "verified", verifiedAt: verifiedAt},
		{name: "legacy exempt", exempt: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			const email = "ansel@example.com"
			memory := testutil.NewMemory()
			id := uuid.New()
			memory.Users[id] = gen.AppUser{
				ID:                      id,
				Email:                   email,
				EmailVerifiedAt:         pgtype.Timestamptz{Time: scenario.verifiedAt, Valid: !scenario.verifiedAt.IsZero()},
				EmailVerificationExempt: scenario.exempt,
			}
			storer := auth.NewStorer(testutil.Store{Querier: memory})
			loaded, err := storer.Load(test.Context(), email)
			if err != nil {
				test.Fatal(err)
			}
			account := loaded.(*auth.User)
			if !account.EmailVerifiedAt.Equal(scenario.verifiedAt) || account.EmailVerificationExempt != scenario.exempt {
				test.Errorf("verification state = (%v, %t), want (%v, %t)",
					account.EmailVerifiedAt, account.EmailVerificationExempt, scenario.verifiedAt, scenario.exempt)
			}
		})
	}
}

func TestSaveOAuth2RefusesAStaleCreate(test *testing.T) {
	memory := testutil.NewMemory()
	storer := auth.NewStorer(testutil.Store{Querier: memory})
	ctx := test.Context()

	pending, err := storer.NewFromOAuth2(ctx, "google", googleDetails("g-1", "ansel@example.com"))
	if err != nil {
		test.Fatal(err)
	}
	if id := pending.(*auth.User).ID; id != uuid.Nil {
		test.Fatalf("a new account has id %v", id)
	}
	// A registration takes the address after NewFromOAuth2 looked at it.
	if err := storer.Create(ctx, &auth.User{Email: "ansel@example.com", PasswordHash: "hash"}); err != nil {
		test.Fatal(err)
	}

	if err := storer.SaveOAuth2(ctx, pending); !errors.Is(err, auth.ErrIdentityConflict) {
		test.Fatalf("SaveOAuth2 = %v, want ErrIdentityConflict", err)
	}
	if len(memory.Users) != 1 {
		test.Fatalf("%d accounts, want 1", len(memory.Users))
	}
	for _, user := range memory.Users {
		if user.Oauth2Uid != nil || user.Oauth2Provider != nil {
			test.Fatalf("the password account got an identity: %v %v", user.Oauth2Provider, user.Oauth2Uid)
		}
	}
}

func TestSaveOAuth2NeverChangesAnIdentity(test *testing.T) {
	memory := testutil.NewMemory()
	storer := auth.NewStorer(testutil.Store{Querier: memory})
	ctx := test.Context()

	linked, err := storer.NewFromOAuth2(ctx, "google", googleDetails("g-1", "ansel@example.com"))
	if err != nil {
		test.Fatal(err)
	}
	if err := storer.SaveOAuth2(ctx, linked); err != nil {
		test.Fatal(err)
	}
	if err := storer.Create(ctx, &auth.User{Email: "dorothea@example.com", PasswordHash: "hash"}); err != nil {
		test.Fatal(err)
	}

	for _, scenario := range []struct{ name, email, provider, uid string }{
		{"a linked row gets a second identity", "ansel@example.com", "google", "g-2"},
		{"an unlinked row gets an identity", "dorothea@example.com", "google", "g-2"},
	} {
		stored, err := storer.Load(ctx, scenario.email)
		if err != nil {
			test.Fatal(err)
		}
		account := stored.(*auth.User)
		before := memory.Users[account.ID]
		account.OAuth2Provider, account.OAuth2UID = scenario.provider, scenario.uid
		if err := storer.SaveOAuth2(ctx, account); !errors.Is(err, auth.ErrIdentityConflict) {
			test.Fatalf("%s: SaveOAuth2 = %v, want ErrIdentityConflict", scenario.name, err)
		}
		if after := memory.Users[account.ID]; !sameIdentity(before, after) {
			test.Fatalf("%s: identity changed", scenario.name)
		}
	}
}

func TestGoogleTokenReturnsAccountConflictWhenInsertConflicts(test *testing.T) {
	memory := testutil.NewMemory()
	accounts, err := auth.New(testutil.Store{Querier: conflictingGoogleInsert{Memory: memory}}, auth.Config{
		Google: auth.GoogleConfig{
			MobileClientIDs: []string{mobileClient},
			VerifyIDToken:   func(context.Context, string) (auth.GoogleIDClaims, error) { return tokenClaims(), nil },
		},
	})
	if err != nil {
		test.Fatal(err)
	}
	router := gin.New()
	accounts.Mount(router)

	request := httptest.NewRequest(http.MethodPost, "/auth/google/token", strings.NewReader(`{"idToken":"test-token"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		test.Fatal(err)
	}
	if response.Code != http.StatusConflict || problem.Code != "account_conflict" {
		test.Fatalf("status=%d code=%s, want 409 account_conflict", response.Code, problem.Code)
	}
}

type conflictingGoogleInsert struct{ *testutil.Memory }

func (conflictingGoogleInsert) InsertUser(context.Context, gen.InsertUserParams) (gen.AppUser, error) {
	return gen.AppUser{}, &pgconn.PgError{Code: "23505"}
}

func sameIdentity(left, right gen.AppUser) bool {
	same := func(first, second *string) bool {
		return first == nil && second == nil || first != nil && second != nil && *first == *second
	}
	return same(left.Oauth2Provider, right.Oauth2Provider) && same(left.Oauth2Uid, right.Oauth2Uid)
}

func TestSaveWithoutCredentialSettersKeepsCredentialsAndChallenge(test *testing.T) {
	memory := testutil.NewMemory()
	id, oldHash, newHash, selector := uuid.New(), "old-hash", "new-hash", "recover-selector"
	memory.Users[id] = gen.AppUser{ID: id, Email: "ansel@example.com", PasswordHash: &oldHash}
	storer := auth.NewStorer(testutil.Store{Querier: memory})
	loaded, err := storer.Load(test.Context(), "ansel@example.com") // a login holds this stale value
	if err != nil {
		test.Fatal(err)
	}

	// A reset commits meanwhile: new hash, fresh challenge, a recovery selector.
	user := memory.Users[id]
	user.PasswordHash, user.RecoverSelector = &newHash, &selector
	user.EmailVerificationSelector = []byte("challenge")
	memory.Users[id] = user

	account := loaded.(*auth.User)
	account.AttemptCount = 1 // lock-style write
	if err := storer.Save(test.Context(), account); err != nil {
		test.Fatal(err)
	}
	saved := memory.Users[id]
	if *saved.PasswordHash != newHash || saved.RecoverSelector == nil || *saved.RecoverSelector != selector || string(saved.EmailVerificationSelector) != "challenge" {
		test.Fatalf("a lock-style save changed credentials or the challenge: %+v", saved)
	}
	if saved.AttemptCount != 1 {
		test.Fatalf("attempt count = %d, want 1", saved.AttemptCount)
	}
}

func TestSaveAfterAPasswordSetterWritesTheHashAndClearsTheChallenge(test *testing.T) {
	memory := testutil.NewMemory()
	id, oldHash := uuid.New(), "old-hash"
	memory.Users[id] = gen.AppUser{ID: id, Email: "ansel@example.com", PasswordHash: &oldHash, EmailVerificationSelector: []byte("challenge")}
	storer := auth.NewStorer(testutil.Store{Querier: memory})
	loaded, err := storer.Load(test.Context(), "ansel@example.com")
	if err != nil {
		test.Fatal(err)
	}
	account := loaded.(*auth.User)
	account.PutPassword("new-hash")
	if err := storer.Save(test.Context(), account); err != nil {
		test.Fatal(err)
	}
	if saved := memory.Users[id]; *saved.PasswordHash != "new-hash" || saved.EmailVerificationSelector != nil {
		test.Fatalf("recovery-style save: %+v", saved)
	}
	account.AttemptCount = 2 // the flag is spent: a later save is login bookkeeping again
	memory.Users[id] = func() gen.AppUser { user := memory.Users[id]; user.PasswordHash = &oldHash; return user }()
	if err := storer.Save(test.Context(), account); err != nil {
		test.Fatal(err)
	}
	if *memory.Users[id].PasswordHash != oldHash {
		test.Fatal("a save after a save must not write credentials again")
	}
}

func TestRecoveryStartWritesOnlyTheRecoveryFields(test *testing.T) {
	memory := testutil.NewMemory()
	id, oldHash, newHash := uuid.New(), "old-hash", "new-hash"
	memory.Users[id] = gen.AppUser{ID: id, Email: "ansel@example.com", PasswordHash: &oldHash}
	storer := auth.NewStorer(testutil.Store{Querier: memory})
	loaded, err := storer.Load(test.Context(), "ansel@example.com") // recovery start holds this stale value
	if err != nil {
		test.Fatal(err)
	}
	user := memory.Users[id] // a reset commits meanwhile
	user.PasswordHash, user.EmailVerificationSelector = &newHash, []byte("challenge")
	memory.Users[id] = user

	account := loaded.(*auth.User)
	expiry := time.Now().Add(time.Hour).UTC()
	account.PutRecoverSelector("selector")
	account.PutRecoverVerifier("verifier")
	account.PutRecoverExpiry(expiry)
	if err := storer.Save(test.Context(), account); err != nil {
		test.Fatal(err)
	}
	saved := memory.Users[id]
	if saved.RecoverSelector == nil || *saved.RecoverSelector != "selector" || saved.RecoverVerifier == nil || *saved.RecoverVerifier != "verifier" || !saved.RecoverTokenExpiry.Valid {
		test.Fatalf("the recovery fields were not written: %+v", saved)
	}
	if *saved.PasswordHash != newHash || string(saved.EmailVerificationSelector) != "challenge" {
		test.Fatalf("recovery start restored the old hash or cleared the challenge: %+v", saved)
	}
}

func TestRecoveryEndWritesTheHashAndClearsRecoveryAndChallenge(test *testing.T) {
	memory := testutil.NewMemory()
	id, oldHash, selector, verifier := uuid.New(), "old-hash", "selector", "verifier"
	memory.Users[id] = gen.AppUser{
		ID: id, Email: "ansel@example.com", PasswordHash: &oldHash, RecoverSelector: &selector, RecoverVerifier: &verifier,
		RecoverTokenExpiry: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}, EmailVerificationSelector: []byte("challenge"),
	}
	storer := auth.NewStorer(testutil.Store{Querier: memory})
	loaded, err := storer.Load(test.Context(), "ansel@example.com")
	if err != nil {
		test.Fatal(err)
	}
	account := loaded.(*auth.User)
	account.PutPassword("new-hash")
	account.PutRecoverSelector("")
	account.PutRecoverVerifier("")
	account.PutRecoverExpiry(time.Time{})
	if err := storer.Save(test.Context(), account); err != nil {
		test.Fatal(err)
	}
	saved := memory.Users[id]
	if *saved.PasswordHash != "new-hash" || saved.RecoverSelector != nil || saved.RecoverVerifier != nil || saved.RecoverTokenExpiry.Valid || saved.EmailVerificationSelector != nil {
		test.Fatalf("recovery end: %+v", saved)
	}
}
