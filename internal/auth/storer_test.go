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
