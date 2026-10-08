package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
)

// browser talks to the API over HTTP with a cookie jar, like a front end with credentials: "include".
type browser struct {
	test   *testing.T
	server *httptest.Server
	client *http.Client
}

func (harness *harness) browser() *browser {
	server := httptest.NewServer(harness.app)
	harness.test.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	return &browser{test: harness.test, server: server, client: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (browser *browser) call(method, path string, body any) response {
	browser.test.Helper()
	payload, _ := json.Marshal(body)
	request, _ := http.NewRequest(method, browser.server.URL+path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	reply, err := browser.client.Do(request)
	if err != nil {
		browser.test.Fatal(err)
	}
	defer reply.Body.Close()
	result := response{Status: reply.StatusCode}
	_ = json.NewDecoder(reply.Body).Decode(&result.Body)
	return result
}

func TestAccountLifecycle(test *testing.T) {
	harness := newHarness(test)
	browser := harness.browser()
	account := map[string]string{"email": "Ansel@Example.com", "password": "Zone-System5", "name": "Ansel"}

	registered := harness.expect(browser.call("POST", "/auth/register", account), http.StatusCreated)
	userID := asObject(registered.Body["user"])["id"]
	harness.expect(browser.call("POST", "/auth/register", account), http.StatusConflict)

	me := harness.expect(browser.call("GET", "/auth/me", nil), http.StatusOK)
	if me.Body["id"] != userID || me.Body["email"] != "ansel@example.com" || me.Body["name"] != "Ansel" {
		test.Fatalf("me = %v", me.Body)
	}

	harness.expect(browser.call("POST", "/auth/logout", nil), http.StatusOK)
	harness.expect(browser.call("GET", "/auth/me", nil), http.StatusUnauthorized)

	phone := harness.browser()
	harness.expect(phone.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Zone-System5"}), http.StatusOK)
	for attempt := 1; attempt < 5; attempt++ {
		harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Wrong-Pass1"}), http.StatusUnauthorized)
	}
	harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Wrong-Pass1"}), http.StatusTooManyRequests)
	harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Zone-System5"}), http.StatusTooManyRequests)

	harness.expect(browser.call("POST", "/auth/recover", map[string]string{"email": "ansel@example.com"}), http.StatusOK)
	token := harness.mail.RecoverToken()
	if token == "" {
		test.Fatalf("no recovery email: %+v", harness.mail.Sent())
	}
	harness.expect(browser.call("POST", "/auth/recover/end", map[string]string{"token": token, "password": "Fresh-Neg4tive"}), http.StatusOK)
	harness.expect(browser.call("POST", "/auth/recover/end", map[string]string{"token": token, "password": "Fresh-Neg4tive"}), http.StatusUnprocessableEntity)
	harness.expect(phone.call("GET", "/auth/me", nil), http.StatusUnauthorized) // the reset ended every session

	harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Zone-System5"}), http.StatusUnauthorized)
	harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Fresh-Neg4tive"}), http.StatusOK)
	harness.expect(browser.call("GET", "/auth/me", nil), http.StatusOK)

	// Accounts stay out of the audit log, which would otherwise keep password hashes.
	var audited int
	if err := harness.pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE entity_type = 'app_user'").Scan(&audited); err != nil || audited != 0 {
		test.Fatalf("app_user changes must not be audited: count=%d err=%v", audited, err)
	}
}

// signInWithGoogle walks the browser through Google sign-in and returns where it lands.
func (browser *browser) signInWithGoogle() string {
	browser.test.Helper()
	start, err := browser.client.Get(browser.server.URL + "/auth/oauth2/google?redir=/rolls")
	if err != nil {
		browser.test.Fatal(err)
	}
	start.Body.Close()
	location, _ := url.Parse(start.Header.Get("Location"))
	callback, err := browser.client.Get(browser.server.URL + "/auth/oauth2/callback/google?code=good-code&state=" + url.QueryEscape(location.Query().Get("state")))
	if err != nil {
		browser.test.Fatal(err)
	}
	callback.Body.Close()
	return callback.Header.Get("Location")
}

func TestGoogleSignInCreatesThenLinksAccounts(test *testing.T) {
	harness := newHarness(test)
	browser := harness.browser()
	if landing := browser.signInWithGoogle(); landing != "http://app.test/rolls" {
		test.Fatalf("landed on %q", landing)
	}
	me := harness.expect(browser.call("GET", "/auth/me", nil), http.StatusOK)
	if me.Body["email"] != "ansel@example.com" || me.Body["name"] != "Ansel Adams" {
		test.Fatalf("me = %v", me.Body)
	}
	harness.expect(browser.call("POST", "/cameras", map[string]any{"brand": "Leica", "model": "M6", "mount": "M", "hasFixedLens": false}), http.StatusCreated)

	var passwordHash *string
	if err := harness.pool.QueryRow(context.Background(), "SELECT password_hash FROM app_user WHERE email = 'ansel@example.com'").Scan(&passwordHash); err != nil || passwordHash != nil {
		test.Fatalf("a Google-only account has no password: hash=%v err=%v", passwordHash, err)
	}

	// A password account with the same verified email is linked rather than duplicated.
	harness.google.Account = map[string]any{"sub": "g-2", "email": "dorothea@example.com", "email_verified": true, "name": "Dorothea"}
	registered := harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": "dorothea@example.com", "password": "Zone-System5"}), http.StatusCreated)
	linked := harness.browser()
	linked.signInWithGoogle()
	if me := harness.expect(linked.call("GET", "/auth/me", nil), http.StatusOK); me.Body["id"] != asObject(registered.Body["user"])["id"] {
		test.Fatalf("me = %v, want the registered account", me.Body)
	}
}
