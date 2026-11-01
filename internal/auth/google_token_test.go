package auth_test

import (
	"context"
	"errors"
	"net/http"
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

func TestGoogleTokenLinksTheAccountWithTheSameVerifiedEmail(test *testing.T) {
	memory := testutil.NewMemory()
	registered := newClient(test, memory, &testutil.Mailbox{})
	account := registered.expect(registered.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "").body["user"].(map[string]any)

	client := tokenClient(test, memory, tokenClaims())
	if me := client.expect(client.signInWithToken(goodToken), http.StatusOK, ""); me.body["user"].(map[string]any)["id"] != account["id"] {
		test.Fatalf("signed in as %v, want %v", me.body, account)
	}
	if len(memory.Users) != 1 {
		test.Fatalf("users = %d, want 1", len(memory.Users))
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
