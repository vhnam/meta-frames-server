package auth_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/testutil"
)

const (
	mobileClient = "ios-client"
	goodToken    = "good-id-token"
)

// tokenClaims is what the fake verifier answers for goodToken.
func tokenClaims() auth.GoogleIDClaims {
	return auth.GoogleIDClaims{
		Audience: mobileClient, Subject: "g-1", Email: "Ansel@Example.com", EmailVerified: true, Name: "Ansel Adams",
	}
}

func tokenClient(test *testing.T, memory *testutil.Memory, claims auth.GoogleIDClaims) *session {
	test.Helper()
	return newClientWith(test, memory, auth.Config{
		AppURL: "http://app.test",
		Mailer: &testutil.Mailbox{},
		Google: auth.GoogleConfig{
			MobileClientIDs: []string{mobileClient},
			VerifyIDToken: func(_ context.Context, idToken string) (auth.GoogleIDClaims, error) {
				if idToken != goodToken {
					return auth.GoogleIDClaims{}, errors.New("bad signature")
				}
				return claims, nil
			},
		},
	})
}

func (client *session) signInWithToken(idToken string) reply {
	client.test.Helper()
	return client.call("POST", "/auth/google/token", map[string]string{"idToken": idToken})
}

func TestGoogleTokenCreatesAnAccountAndStartsASession(test *testing.T) {
	memory := testutil.NewMemory()
	client := tokenClient(test, memory, tokenClaims())

	result := client.expect(client.signInWithToken(goodToken), http.StatusOK, "")
	user := result.body["user"].(map[string]any)
	if user["email"] != email || user["name"] != "Ansel Adams" {
		test.Fatalf("user = %v", user)
	}
	me := client.expect(client.me(), http.StatusOK, "")
	if me.body["id"] != user["id"] {
		test.Fatalf("me = %v, want %v", me.body, user)
	}

	// Signing in again reuses the account and replaces the session.
	client.expect(client.signInWithToken(goodToken), http.StatusOK, "")
	if len(memory.Users) != 1 {
		test.Fatalf("users = %d, want 1", len(memory.Users))
	}
	if len(memory.Sessions) != 1 {
		test.Fatalf("sessions = %d, want 1", len(memory.Sessions))
	}
}

func TestGoogleTokenRefusesTheEmailOfAPasswordAccount(test *testing.T) {
	memory := testutil.NewMemory()
	registered := newClient(test, memory, &testutil.Mailbox{})
	registered.expect(registered.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	before := onlyUser(test, memory)

	client := tokenClient(test, memory, tokenClaims())
	client.expect(client.signInWithToken(goodToken), http.StatusConflict, "account_conflict")
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	if after := onlyUser(test, memory); !reflect.DeepEqual(after, before) {
		test.Fatalf("a refused sign-in changed the account:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestGoogleTokenRefusesALockedPasswordAccountWithoutAWrite(test *testing.T) {
	memory := testutil.NewMemory()
	registered := newClient(test, memory, &testutil.Mailbox{})
	registered.expect(registered.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	for id, user := range memory.Users {
		user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		memory.Users[id] = user
	}
	before := onlyUser(test, memory)

	client := tokenClient(test, memory, tokenClaims())
	client.expect(client.signInWithToken(goodToken), http.StatusConflict, "account_conflict")
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	if after := onlyUser(test, memory); !reflect.DeepEqual(after, before) {
		test.Fatalf("a refused sign-in changed the locked account:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestGoogleTokenReturningIdentityKeepsItsAccountWhenItsEmailMatchesAnother(test *testing.T) {
	memory := testutil.NewMemory()
	first := tokenClient(test, memory, tokenClaims())
	googleUser := first.expect(first.signInWithToken(goodToken), http.StatusOK, "").body["user"].(map[string]any)

	registered := newClient(test, memory, &testutil.Mailbox{})
	const otherEmail = "dorothea@example.com"
	registered.expect(registered.call("POST", "/auth/register", credentials(otherEmail, password)), http.StatusCreated, "")
	accountsBefore := maps.Clone(memory.Users)

	moved := tokenClaims()
	moved.Email = "Dorothea@Example.com"
	client := tokenClient(test, memory, moved)
	signedIn := client.expect(client.signInWithToken(goodToken), http.StatusOK, "").body["user"].(map[string]any)
	if signedIn["id"] != googleUser["id"] {
		test.Fatalf("signed in as %v, want the Google account %v", signedIn, googleUser)
	}
	for id, user := range memory.Users {
		switch user.Email {
		case email:
			if user.Oauth2Uid == nil {
				test.Fatal("the Google account lost its identity")
			}
		case otherEmail:
			if !reflect.DeepEqual(user, accountsBefore[id]) {
				test.Fatal("the password account changed")
			}
		default:
			test.Fatalf("unexpected account %q", user.Email)
		}
	}
	if len(memory.Users) != 2 {
		test.Fatalf("users = %d, want 2", len(memory.Users))
	}
}

func TestGoogleTokenReturningIdentityDoesNotRequireAVerifiedEmailClaim(test *testing.T) {
	memory := testutil.NewMemory()
	claims := tokenClaims()
	first := tokenClient(test, memory, claims)
	first.expect(first.signInWithToken(goodToken), http.StatusOK, "")
	before := onlyUser(test, memory)

	claims.EmailVerified = false
	returning := tokenClient(test, memory, claims)
	returning.expect(returning.signInWithToken(goodToken), http.StatusOK, "")
	me := returning.expect(returning.me(), http.StatusOK, "")
	if me.body["id"] != before.ID.String() {
		test.Fatalf("session account = %v, want %s", me.body["id"], before.ID)
	}
}

func TestGoogleTokenChecksEmailVerificationBeforeAccountConflict(test *testing.T) {
	memory := testutil.NewMemory()
	claims := tokenClaims()
	claims.EmailVerified = false
	client := tokenClient(test, memory, claims)
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	before := onlyUser(test, memory)
	sessionsBefore := maps.Clone(memory.Sessions)

	client.expect(client.signInWithToken(goodToken), http.StatusForbidden, "email_not_verified")
	if after := onlyUser(test, memory); !reflect.DeepEqual(after, before) {
		test.Fatal("the account changed after refusal")
	}
	if !reflect.DeepEqual(memory.Sessions, sessionsBefore) {
		test.Fatal("the sessions changed after refusal")
	}
}

func TestGoogleTokenAcceptsTheWebClientAsAudience(test *testing.T) {
	claims := tokenClaims()
	claims.Audience = "web-client"
	client := newClientWith(test, testutil.NewMemory(), auth.Config{
		AppURL: "http://app.test",
		Mailer: &testutil.Mailbox{},
		Google: auth.GoogleConfig{
			ClientID: "web-client", ClientSecret: "secret",
			VerifyIDToken: func(context.Context, string) (auth.GoogleIDClaims, error) { return claims, nil },
		},
	})
	client.expect(client.signInWithToken(goodToken), http.StatusOK, "")
}

func TestGoogleTokenAcceptsEachMobileClient(test *testing.T) {
	mobile := []string{"android-client", "ios-client"}
	for _, audience := range mobile {
		test.Run(audience, func(test *testing.T) {
			claims := tokenClaims()
			claims.Audience = audience
			client := newClientWith(test, testutil.NewMemory(), auth.Config{
				AppURL: "http://app.test",
				Mailer: &testutil.Mailbox{},
				Google: auth.GoogleConfig{
					MobileClientIDs: mobile,
					VerifyIDToken:   func(context.Context, string) (auth.GoogleIDClaims, error) { return claims, nil },
				},
			})
			client.expect(client.signInWithToken(goodToken), http.StatusOK, "")
			client.expect(client.me(), http.StatusOK, "")
		})
	}
}

func TestGoogleTokenRefusals(test *testing.T) {
	otherClient := tokenClaims()
	otherClient.Audience = "someone-elses-app"
	unverified := tokenClaims()
	unverified.EmailVerified = false

	for _, refused := range []struct {
		name   string
		token  string
		claims auth.GoogleIDClaims
		status int
		code   string
	}{
		{"bad signature", "forged", tokenClaims(), http.StatusUnauthorized, "invalid_google_token"},
		{"token for another app", goodToken, otherClient, http.StatusUnauthorized, "invalid_google_token"},
		{"unverified email", goodToken, unverified, http.StatusForbidden, "email_not_verified"},
	} {
		test.Run(refused.name, func(test *testing.T) {
			memory := testutil.NewMemory()
			client := tokenClient(test, memory, refused.claims)
			client.expect(client.signInWithToken(refused.token), refused.status, refused.code)
			client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
			if len(memory.Users) != 0 {
				test.Fatal("a refused sign-in must not create an account")
			}
		})
	}
}

func TestGoogleTokenRefusesAnEmailLinkedToAnotherGoogleAccount(test *testing.T) {
	memory := testutil.NewMemory()
	first := tokenClient(test, memory, tokenClaims())
	first.expect(first.signInWithToken(goodToken), http.StatusOK, "")

	other := tokenClaims()
	other.Subject = "g-2"
	second := tokenClient(test, memory, other)
	second.expect(second.signInWithToken(goodToken), http.StatusConflict, "account_conflict")
}

func TestGoogleTokenRefusesALockedAccount(test *testing.T) {
	memory := testutil.NewMemory()
	client := tokenClient(test, memory, tokenClaims())
	client.expect(client.signInWithToken(goodToken), http.StatusOK, "")
	for id, user := range memory.Users {
		user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		memory.Users[id] = user
	}

	again := tokenClient(test, memory, tokenClaims())
	again.expect(again.signInWithToken(goodToken), http.StatusTooManyRequests, "account_locked")
	again.expect(again.me(), http.StatusUnauthorized, "unauthorized")
}

func TestGoogleConfigReturnsTheWebClientAndNoSecret(test *testing.T) {
	client := newClientWith(test, testutil.NewMemory(), auth.Config{
		AppURL: "http://app.test",
		Mailer: &testutil.Mailbox{},
		Google: auth.GoogleConfig{ClientID: "web-client", ClientSecret: "secret", MobileClientIDs: []string{mobileClient}},
	})
	config := client.expect(client.call("GET", "/auth/google/config", nil), http.StatusOK, "")
	if len(config.body) != 1 || config.body["serverClientId"] != "web-client" {
		test.Fatalf("config = %v", config.body)
	}
}

func TestGoogleConfigAnswers404WithoutAWebClient(test *testing.T) {
	client := tokenClient(test, testutil.NewMemory(), tokenClaims())
	client.expect(client.call("GET", "/auth/google/config", nil), http.StatusNotFound, "not_configured")
}

func TestGoogleTokenRequiresAToken(test *testing.T) {
	client := tokenClient(test, testutil.NewMemory(), tokenClaims())
	client.expect(client.call("POST", "/auth/google/token", map[string]string{}), http.StatusBadRequest, "")
}

func TestGoogleTokenAnswers404WhenNotConfigured(test *testing.T) {
	client := newClient(test, testutil.NewMemory(), &testutil.Mailbox{})
	client.expect(client.signInWithToken(goodToken), http.StatusNotFound, "not_configured")
}

func TestGoogleTokenRefreshesThePictureAndDropsANonHTTPSOne(test *testing.T) {
	memory := testutil.NewMemory()
	claims := tokenClaims()
	claims.Picture = "https://lh3.googleusercontent.com/a/old"
	tokenClient(test, memory, claims).signInWithToken(goodToken)

	claims.Picture = "https://lh3.googleusercontent.com/a/new"
	client := tokenClient(test, memory, claims)
	user := client.expect(client.signInWithToken(goodToken), http.StatusOK, "").body["user"].(map[string]any)
	if user["avatarUrl"] != claims.Picture {
		test.Fatalf("user = %v, want the new picture", user)
	}

	// A picture that is not https is never stored; the last good one stays.
	claims.Picture = "javascript:alert(1)"
	client = tokenClient(test, memory, claims)
	user = client.expect(client.signInWithToken(goodToken), http.StatusOK, "").body["user"].(map[string]any)
	if user["avatarUrl"] != "https://lh3.googleusercontent.com/a/new" {
		test.Fatalf("user = %v, want the last https picture", user)
	}
}
