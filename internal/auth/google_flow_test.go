package auth_test

import (
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

const callbackURL = "http://api.test/auth/oauth2/callback/google"

type fakeGoogle struct{ *testutil.FakeGoogle }

func newFakeGoogle(test *testing.T) *fakeGoogle {
	return &fakeGoogle{testutil.NewFakeGoogle(test, callbackURL)}
}

func (google *fakeGoogle) client(test *testing.T, memory *testutil.Memory) *session {
	return newClientWith(test, memory, auth.Config{
		AppURL: "http://app.test",
		APIURL: "http://api.test",
		Mailer: &testutil.Mailbox{},
		Google: google.Config(),
	})
}

func (client *session) get(path string) *http.Response {
	client.test.Helper()
	response, err := client.http.Get(client.server.URL + path)
	if err != nil {
		client.test.Fatal(err)
	}
	_ = response.Body.Close()
	return response
}

// startGoogle follows the start route and returns the state Google would echo back.
func (client *session) startGoogle(redir string) string {
	client.test.Helper()
	response := client.get("/auth/oauth2/google?redir=" + url.QueryEscape(redir))
	location, _ := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != http.StatusFound || location.Path != "/auth" || location.Query().Get("redirect_uri") != callbackURL ||
		location.Query().Get("prompt") != "select_account" {
		client.test.Fatalf("start: status=%d location=%s", response.StatusCode, location)
	}
	return location.Query().Get("state")
}

// signInWithGoogle runs the whole flow and returns where the browser ends up.
func (client *session) signInWithGoogle(redir string) string {
	client.test.Helper()
	state := client.startGoogle(redir)
	response := client.get("/auth/oauth2/callback/google?code=good-code&state=" + url.QueryEscape(state))
	if response.StatusCode != http.StatusFound {
		client.test.Fatalf("callback: status=%d", response.StatusCode)
	}
	return response.Header.Get("Location")
}

func TestGoogleSignInCreatesAnAccount(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	client := google.client(test, memory)

	if landing := client.signInWithGoogle("/rolls?tab=open"); landing != "http://app.test/rolls?tab=open" {
		test.Fatalf("landed on %q", landing)
	}
	me := client.expect(client.me(), http.StatusOK, "")
	if me.body["email"] != email || me.body["name"] != "Ansel Adams" {
		test.Fatalf("me = %v", me.body)
	}
	if client.cookieNamed(auth.OAuth2Cookie) != nil {
		test.Fatal("the sign-in state cookie must be cleared after the callback")
	}

	// Signing in again reuses the account, and the account has no password to guess.
	again := google.client(test, memory)
	again.signInWithGoogle("/")
	if again.expect(again.me(), http.StatusOK, "").body["id"] != me.body["id"] || len(memory.Users) != 1 {
		test.Fatal("a second sign-in must reuse the account")
	}
	again.expect(again.call("POST", "/auth/login", credentials(email, password)), http.StatusUnauthorized, "invalid_credentials")
}

// onlyUser returns the single account in memory.
func onlyUser(test *testing.T, memory *testutil.Memory) gen.AppUser {
	test.Helper()
	if len(memory.Users) != 1 {
		test.Fatalf("users = %d, want 1", len(memory.Users))
	}
	for _, user := range memory.Users {
		return user
	}
	return gen.AppUser{}
}

func TestGoogleSignInRefusesTheEmailOfAPasswordAccount(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	registered := newClient(test, memory, &testutil.Mailbox{})
	registered.expect(registered.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	before := onlyUser(test, memory)

	client := google.client(test, memory)
	if landing := client.signInWithGoogle("/"); landing != "http://app.test/login?error=account_conflict" {
		test.Fatalf("a Google sign-in for a password account landed on %q", landing)
	}
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	if after := onlyUser(test, memory); !reflect.DeepEqual(after, before) {
		test.Fatalf("a refused sign-in changed the account:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestGoogleSignInRefusesALockedPasswordAccountWithoutAWrite(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	registered := newClient(test, memory, &testutil.Mailbox{})
	registered.expect(registered.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	for id, user := range memory.Users {
		user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		memory.Users[id] = user
	}
	before := onlyUser(test, memory)

	client := google.client(test, memory)
	if landing := client.signInWithGoogle("/"); landing != "http://app.test/login?error=account_conflict" {
		test.Fatalf("a locked password account landed on %q", landing)
	}
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	if after := onlyUser(test, memory); !reflect.DeepEqual(after, before) {
		test.Fatalf("a refused sign-in changed the locked account:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestGoogleSignInFailuresReturnToTheLoginPage(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	client := google.client(test, memory)

	google.Account["email_verified"] = false
	if landing := client.signInWithGoogle("/"); landing != "http://app.test/login?error=email_not_verified" {
		test.Fatalf("unverified email landed on %q", landing)
	}
	google.Account["email_verified"] = true

	state := client.startGoogle("/")
	if landing := client.get("/auth/oauth2/callback/google?error=access_denied&state=" + url.QueryEscape(state)).Header.Get("Location"); landing != "http://app.test/login?error=google_denied" {
		test.Fatalf("cancelled sign-in landed on %q", landing)
	}
	client.startGoogle("/")
	if landing := client.get("/auth/oauth2/callback/google?code=good-code&state=forged").Header.Get("Location"); landing != "http://app.test/login?error=google_failed" {
		test.Fatalf("forged state landed on %q", landing)
	}
	stranger := google.client(test, memory) // never started the flow, so has no state cookie
	if landing := stranger.get("/auth/oauth2/callback/google?code=good-code&state=x").Header.Get("Location"); landing != "http://app.test/login?error=google_failed" {
		test.Fatalf("callback without a started flow landed on %q", landing)
	}
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	if len(memory.Users) != 0 {
		test.Fatal("failed sign-ins must not create accounts")
	}
}

func TestGoogleSignInRefusesALockedAccount(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	google.client(test, memory).signInWithGoogle("/")
	for id, user := range memory.Users {
		user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		memory.Users[id] = user
	}

	google.Account["name"], google.Account["picture"] = "New Name", "https://lh3.googleusercontent.com/a/new"
	usersBefore := maps.Clone(memory.Users)
	sessionsBefore := maps.Clone(memory.Sessions)

	for _, verified := range []bool{true, false} {
		google.Account["email_verified"] = verified
		again := google.client(test, memory)
		if landing := again.signInWithGoogle("/rolls"); landing != "http://app.test/login?error=account_locked" {
			test.Fatalf("a locked account with email_verified=%t landed on %q", verified, landing)
		}
		again.expect(again.me(), http.StatusUnauthorized, "unauthorized")
		if !reflect.DeepEqual(memory.Users, usersBefore) {
			test.Fatalf("a locked account was written with email_verified=%t", verified)
		}
		if !reflect.DeepEqual(memory.Sessions, sessionsBefore) {
			test.Fatalf("sessions changed after refusal with email_verified=%t", verified)
		}
	}
}

func TestGoogleSignInOnlyRedirectsWithinTheFrontEnd(test *testing.T) {
	google := newFakeGoogle(test)
	for _, redir := range []string{"https://evil.example/", "//evil.example", `/\evil.example`, "javascript:alert(1)", "rolls"} {
		client := google.client(test, testutil.NewMemory())
		if landing := client.signInWithGoogle(redir); landing != "http://app.test/" {
			test.Fatalf("redir %q landed on %q", redir, landing)
		}
	}
}

func TestGoogleRoutesAnswer404WhenNotConfigured(test *testing.T) {
	client := newClient(test, testutil.NewMemory(), &testutil.Mailbox{})
	for _, path := range []string{"/auth/oauth2/google", "/auth/oauth2/callback/google"} {
		response := client.get(path)
		if response.StatusCode != http.StatusNotFound {
			test.Fatalf("GET %s = %d", path, response.StatusCode)
		}
	}
}

func (client *session) cookieNamed(name string) *http.Cookie {
	serverURL, _ := url.Parse(client.server.URL)
	for _, cookie := range client.http.Jar.Cookies(serverURL.JoinPath("/auth/oauth2")) {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestGoogleSignInKeepsTheAccountPicture(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	const picture = "https://lh3.googleusercontent.com/a/ansel"
	google.Account["picture"] = picture
	client := google.client(test, memory)
	client.signInWithGoogle("/")
	if me := client.expect(client.me(), http.StatusOK, ""); me.body["avatarUrl"] != picture {
		test.Fatalf("me = %v, want avatarUrl %q", me.body, picture)
	}
}
