package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"meta-frames-server/internal/db/gen"
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
	defer func() { _ = reply.Body.Close() }()
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

func TestRegisterRejectsBadInputAndDuplicates(test *testing.T) {
	harness := newHarness(test)
	browser := harness.browser()
	const email, password = "ansel@example.com", "Zone-System5"

	rejected := []struct {
		name     string
		account  map[string]string
		code     string
		mentions string
	}{
		// The OpenAPI schema refuses a short password before the auth rules run.
		{"password too short", map[string]string{"email": email, "password": "Zn-5a"}, "bad_request", "password"},
		{"password without a digit", map[string]string{"email": email, "password": "Zone-System"}, "validation_failed", "password"},
		{"password without an upper-case letter", map[string]string{"email": email, "password": "zone-system5"}, "validation_failed", "password"},
		{"password without a symbol", map[string]string{"email": email, "password": "ZoneSystem5"}, "validation_failed", "password"},
		{"invalid email", map[string]string{"email": "not-an-email", "password": password}, "validation_failed", "email"},
	}
	for _, rejection := range rejected {
		result := harness.expect(browser.call("POST", "/auth/register", rejection.account), http.StatusBadRequest)
		message, _ := result.Body["message"].(string)
		if result.Body["code"] != rejection.code || !strings.Contains(message, rejection.mentions) {
			test.Fatalf("%s: body = %v, want %s naming %q", rejection.name, result.Body, rejection.code, rejection.mentions)
		}
	}

	// None of the rejected attempts left an account behind: the same email still registers.
	harness.expect(browser.call("POST", "/auth/register", map[string]string{"email": email, "password": password}), http.StatusCreated)

	// A second account with the same email, in any letter case, is refused and does not touch the first.
	other := harness.browser()
	taken := harness.expect(other.call("POST", "/auth/register", map[string]string{"email": "ANSEL@Example.com", "password": "Other-Pass7"}), http.StatusConflict)
	if message, _ := taken.Body["message"].(string); taken.Body["code"] != "email_taken" || message == "" {
		test.Fatalf("duplicate body = %v, want email_taken with a message", taken.Body)
	}
	harness.expect(other.call("GET", "/auth/me", nil), http.StatusUnauthorized)
	harness.expect(other.call("POST", "/auth/login", map[string]string{"email": email, "password": "Other-Pass7"}), http.StatusUnauthorized)
	harness.expect(other.call("POST", "/auth/login", map[string]string{"email": email, "password": password}), http.StatusOK)
}

// signInWithGoogle walks the browser through Google sign-in and returns where it lands.
func (browser *browser) signInWithGoogle() string {
	browser.test.Helper()
	start, err := browser.client.Get(browser.server.URL + "/auth/oauth2/google?redir=/rolls")
	if err != nil {
		browser.test.Fatal(err)
	}
	_ = start.Body.Close()
	location, _ := url.Parse(start.Header.Get("Location"))
	callback, err := browser.client.Get(browser.server.URL + "/auth/oauth2/callback/google?code=good-code&state=" + url.QueryEscape(location.Query().Get("state")))
	if err != nil {
		browser.test.Fatal(err)
	}
	_ = callback.Body.Close()
	return callback.Header.Get("Location")
}

func TestGoogleSignInCreatesAndRefusesAccounts(test *testing.T) {
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

	// A password account with the same verified email is refused, not linked.
	harness.google.Account = map[string]any{"sub": "g-2", "email": "dorothea@example.com", "email_verified": true, "name": "Dorothea"}
	harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": "dorothea@example.com", "password": "Zone-System5"}), http.StatusCreated)
	var hashBefore *string
	if err := harness.pool.QueryRow(context.Background(), "SELECT password_hash FROM app_user WHERE email = 'dorothea@example.com'").Scan(&hashBefore); err != nil || hashBefore == nil {
		test.Fatalf("the password account needs a hash: hash=%v err=%v", hashBefore, err)
	}
	refused := harness.browser()
	if landing := refused.signInWithGoogle(); landing != "http://app.test/login?error=account_conflict" {
		test.Fatalf("a Google sign-in for a password account landed on %q", landing)
	}
	harness.expect(refused.call("GET", "/auth/me", nil), http.StatusUnauthorized)
	var linked *string
	var hash *string
	if err := harness.pool.QueryRow(context.Background(), "SELECT oauth2_uid, password_hash FROM app_user WHERE email = 'dorothea@example.com'").Scan(&linked, &hash); err != nil || linked != nil || hash == nil || *hash != *hashBefore {
		test.Fatalf("the password account must stay unlinked: uid=%v hash=%v err=%v", linked, hash, err)
	}
}

func TestGoogleSignInLeavesAPasswordAccountUntouched(test *testing.T) {
	harness := newHarness(test)
	owner := harness.browser()
	registered := harness.expect(owner.call("POST", "/auth/register", map[string]string{"email": "dorothea@example.com", "password": "Zone-System5"}), http.StatusCreated)
	userID := asObject(registered.Body["user"])["id"]

	snapshot := func() (hash, updatedAt string, sessions int, linked *string) {
		test.Helper()
		if err := harness.pool.QueryRow(context.Background(), `SELECT password_hash, updated_at::text, oauth2_uid,
			(SELECT count(*) FROM user_session WHERE user_id = app_user.id) FROM app_user WHERE email = 'dorothea@example.com'`).Scan(&hash, &updatedAt, &linked, &sessions); err != nil {
			test.Fatal(err)
		}
		return hash, updatedAt, sessions, linked
	}
	hashBefore, updatedBefore, sessionsBefore, _ := snapshot()

	harness.google.Account = map[string]any{"sub": "g-9", "email": "Dorothea@Example.COM", "email_verified": true, "name": "Not Dorothea"}
	attacker := harness.browser()
	if landing := attacker.signInWithGoogle(); landing != "http://app.test/login?error=account_conflict" {
		test.Fatalf("landed on %q", landing)
	}
	harness.expect(attacker.call("GET", "/auth/me", nil), http.StatusUnauthorized)
	if me := harness.expect(owner.call("GET", "/auth/me", nil), http.StatusOK); me.Body["id"] != userID {
		test.Fatalf("me = %v, want %v", me.Body, userID)
	}
	hashAfter, updatedAfter, sessionsAfter, linked := snapshot()
	if hashAfter != hashBefore || updatedAfter != updatedBefore || sessionsAfter != sessionsBefore || linked != nil {
		test.Fatalf("the account changed: hash same=%v updated %q to %q, sessions %d to %d, uid=%v",
			hashAfter == hashBefore, updatedBefore, updatedAfter, sessionsBefore, sessionsAfter, linked)
	}
	harness.expect(harness.browser().call("POST", "/auth/login", map[string]string{"email": "dorothea@example.com", "password": "Zone-System5"}), http.StatusOK)
}

func TestGoogleSignInRefusesALockedAccountWithoutWriting(test *testing.T) {
	harness := newHarness(test)
	harness.google.Account = map[string]any{"sub": "g-1", "email": "ansel@example.com", "email_verified": true, "name": "Ansel Adams", "picture": "https://example.com/a.png"}
	if landing := harness.browser().signInWithGoogle(); landing != "http://app.test/rolls" {
		test.Fatalf("landed on %q", landing)
	}
	harness.execSQL("UPDATE app_user SET locked_until = now() + interval '1 hour' WHERE email = 'ansel@example.com'")
	snapshot := func() (row [5]string) {
		test.Helper()
		if err := harness.pool.QueryRow(context.Background(), `SELECT coalesce(name, ''), coalesce(avatar_url, ''), coalesce(last_attempt::text, ''), attempt_count::text, updated_at::text
			FROM app_user WHERE email = 'ansel@example.com'`).Scan(&row[0], &row[1], &row[2], &row[3], &row[4]); err != nil {
			test.Fatal(err)
		}
		return row
	}
	before := snapshot()

	harness.google.Account["name"], harness.google.Account["picture"] = "New Name", "https://example.com/b.png"
	locked := harness.browser()
	if landing := locked.signInWithGoogle(); landing != "http://app.test/login?error=account_locked" {
		test.Fatalf("a locked account landed on %q", landing)
	}
	harness.expect(locked.call("GET", "/auth/me", nil), http.StatusUnauthorized)
	if after := snapshot(); after != before {
		test.Fatalf("a locked account was written: %v, was %v", after, before)
	}
}

// UpdateUser only matches on the identity: a caller whose copy is stale writes nothing.
func TestUpdateUserNeedsTheStoredIdentity(test *testing.T) {
	harness := newHarness(test)
	ctx := context.Background()
	queries := gen.New(harness.pool)
	harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": "dorothea@example.com", "password": "Zone-System5"}), http.StatusCreated)
	harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": "ansel@example.com", "password": "Zone-System5"}), http.StatusCreated)
	harness.execSQL("UPDATE app_user SET oauth2_provider = 'google', oauth2_uid = 'g-1' WHERE email = 'ansel@example.com'")
	google := func(uid string) *string { return &uid }

	for _, scenario := range []struct {
		email    string
		provider *string
		uid      *string
	}{
		{"ansel@example.com", google("google"), google("g-2")},    // linked row, other identity
		{"ansel@example.com", nil, nil},                           // linked row, caller thinks it is unlinked
		{"dorothea@example.com", google("google"), google("g-2")}, // unlinked row, caller brings an identity
	} {
		user, err := queries.GetUserByEmail(ctx, scenario.email)
		if err != nil {
			test.Fatal(err)
		}
		_, err = queries.UpdateUser(ctx, gen.UpdateUserParams{ID: user.ID, Oauth2Provider: scenario.provider, Oauth2Uid: scenario.uid})
		if !errors.Is(err, pgx.ErrNoRows) {
			test.Fatalf("%s: UpdateUser = %v, want pgx.ErrNoRows", scenario.email, err)
		}
		after, err := queries.GetUserByEmail(ctx, scenario.email)
		if err != nil {
			test.Fatal(err)
		}
		if !reflect.DeepEqual(after, user) {
			test.Fatalf("%s: the row changed: %+v to %+v", scenario.email, user, after)
		}
	}
}
