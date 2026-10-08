package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/server"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

const (
	email    = "ansel@example.com"
	password = "Zone-System5"
)

type session struct {
	test   *testing.T
	server *httptest.Server
	http   *http.Client
}

type reply struct {
	status int
	body   map[string]any
}

func newClient(test *testing.T, memory *testutil.Memory, mailbox *testutil.Mailbox) *session {
	test.Helper()
	return newClientWith(test, memory, auth.Config{AppURL: "http://app.test/", MailFrom: "no-reply@app.test", Mailer: mailbox})
}

func newClientWith(test *testing.T, memory *testutil.Memory, config auth.Config) *session {
	test.Helper()
	store := testutil.Store{Querier: memory}
	accounts, err := auth.New(store, config)
	if err != nil {
		test.Fatal(err)
	}
	handler, err := server.NewHandler(controllers.New(services.New(store, testutil.NewMemoryFiles(), clock.Fixed{})), accounts, []string{"http://app.test"})
	if err != nil {
		test.Fatal(err)
	}
	testServer := httptest.NewServer(handler)
	test.Cleanup(testServer.Close)
	jar, _ := cookiejar.New(nil)
	return &session{test: test, server: testServer, http: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (client *session) call(method, path string, body any) reply {
	client.test.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request, _ := http.NewRequest(method, client.server.URL+path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		client.test.Fatal(err)
	}
	defer response.Body.Close()
	result := reply{status: response.StatusCode}
	_ = json.NewDecoder(response.Body).Decode(&result.body)
	return result
}

func (client *session) expect(result reply, status int, code string) reply {
	client.test.Helper()
	if result.status != status || (code != "" && result.body["code"] != code) {
		client.test.Fatalf("status=%d body=%v, want %d %s", result.status, result.body, status, code)
	}
	return result
}

func (client *session) me() reply { return client.call("GET", "/auth/me", nil) }

func credentials(email, password string) map[string]string {
	return map[string]string{"email": email, "password": password}
}

func TestRegisterLogsInAndMeReturnsTheUser(test *testing.T) {
	client := newClient(test, testutil.NewMemory(), &testutil.Mailbox{})
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")

	registered := client.expect(client.call("POST", "/auth/register",
		map[string]string{"email": "Ansel@Example.com", "password": password, "name": "Ansel"}), http.StatusCreated, "")
	user := registered.body["user"].(map[string]any)
	if user["email"] != email || user["name"] != "Ansel" || user["id"] == "" || registered.body["message"] == "" {
		test.Fatalf("register body = %v", registered.body)
	}
	if _, leaked := user["password"]; leaked {
		test.Fatal("the password hash must never be returned")
	}

	me := client.expect(client.me(), http.StatusOK, "")
	if me.body["id"] != user["id"] || me.body["email"] != email {
		test.Fatalf("me = %v, want %v", me.body, user)
	}
}

func TestRegisterRejectsBadInput(test *testing.T) {
	client := newClient(test, testutil.NewMemory(), &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, "password1")), http.StatusBadRequest, "validation_failed")
	client.expect(client.call("POST", "/auth/register", credentials("not-an-email", password)), http.StatusBadRequest, "validation_failed")
	client.expect(client.call("POST", "/auth/register", map[string]any{"email": email, "password": password, "admin": true}), http.StatusBadRequest, "bad_request")
	client.expect(client.call("POST", "/auth/register", map[string]string{"email": email}), http.StatusBadRequest, "bad_request")

	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	client.expect(client.call("POST", "/auth/register", credentials("ANSEL@example.com", password)), http.StatusConflict, "email_taken")
}

func TestLoginAndLogout(test *testing.T) {
	memory := testutil.NewMemory()
	signup := newClient(test, memory, &testutil.Mailbox{})
	signup.expect(signup.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	signup.expect(signup.call("POST", "/auth/logout", nil), http.StatusOK, "")
	signup.expect(signup.me(), http.StatusUnauthorized, "unauthorized")

	client := newClient(test, memory, &testutil.Mailbox{})
	wrongPassword := client.expect(client.call("POST", "/auth/login", credentials(email, "Wrong-Pass1")), http.StatusUnauthorized, "invalid_credentials")
	unknownEmail := client.expect(client.call("POST", "/auth/login", credentials("nobody@example.com", password)), http.StatusUnauthorized, "invalid_credentials")
	if wrongPassword.body["message"] != unknownEmail.body["message"] {
		test.Fatal("a wrong password and an unknown email must look the same")
	}
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")

	loggedIn := client.expect(client.call("POST", "/auth/login", credentials("Ansel@Example.com", password)), http.StatusOK, "")
	if loggedIn.body["user"].(map[string]any)["email"] != email {
		test.Fatalf("login body = %v", loggedIn.body)
	}
	client.expect(client.me(), http.StatusOK, "")
	client.expect(client.call("POST", "/auth/logout", nil), http.StatusOK, "")
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
}

func TestPasswordRecoveryAndReset(test *testing.T) {
	mailbox := &testutil.Mailbox{}
	client := newClient(test, testutil.NewMemory(), mailbox)
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	client.expect(client.call("POST", "/auth/logout", nil), http.StatusOK, "")

	unknown := client.expect(client.call("POST", "/auth/recover", map[string]string{"email": "nobody@example.com"}), http.StatusOK, "")
	if len(mailbox.Sent()) != 0 {
		test.Fatal("an unknown email must not get a recovery email")
	}
	known := client.expect(client.call("POST", "/auth/recover", map[string]string{"email": email}), http.StatusOK, "")
	if unknown.body["message"] != known.body["message"] {
		test.Fatal("recovery must answer the same whether or not the account exists")
	}
	sent := mailbox.Sent()
	if len(sent) != 1 || sent[0].To[0] != email || !strings.Contains(sent[0].TextBody, "http://app.test/recover/end?token=") {
		test.Fatalf("sent = %+v", sent)
	}
	token := mailbox.RecoverToken()
	if token == "" {
		test.Fatal("the email must carry a token")
	}

	newPassword := "Fresh-Neg4tive"
	client.expect(client.call("POST", "/auth/recover/end", map[string]string{"token": url.QueryEscape("bogus"), "password": newPassword}), http.StatusUnprocessableEntity, "invalid_token")
	client.expect(client.call("POST", "/auth/recover/end", map[string]string{"token": token, "password": "weakpassword"}), http.StatusBadRequest, "validation_failed")
	client.expect(client.call("POST", "/auth/recover/end", map[string]string{"token": token, "password": newPassword}), http.StatusOK, "")
	client.expect(client.call("POST", "/auth/recover/end", map[string]string{"token": token, "password": newPassword}), http.StatusUnprocessableEntity, "invalid_token")
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")

	client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusUnauthorized, "invalid_credentials")
	client.expect(client.call("POST", "/auth/login", credentials(email, newPassword)), http.StatusOK, "")
}

func TestSessionsThatNoLongerMatchAUserAreAnonymous(test *testing.T) {
	memory := testutil.NewMemory()
	client := newClient(test, memory, &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")

	serverURL, _ := url.Parse(client.server.URL)
	cookies := client.http.Jar.Cookies(serverURL)
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookie {
		test.Fatalf("cookies = %v", cookies)
	}

	tampered, _ := cookiejar.New(nil)
	tampered.SetCookies(serverURL, []*http.Cookie{{Name: auth.SessionCookie, Value: cookies[0].Value[:len(cookies[0].Value)-4] + "AAAA"}})
	stranger := &session{test: test, server: client.server, http: &http.Client{Jar: tampered}}
	stranger.expect(stranger.me(), http.StatusUnauthorized, "unauthorized")

	clear(memory.Users) // the account disappears while the cookie is still valid
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
}

func TestOnlyTheJSONEndpointsAreRouted(test *testing.T) {
	client := newClient(test, testutil.NewMemory(), &testutil.Mailbox{})
	for _, path := range []string{"/auth/login", "/auth/register", "/auth/recover", "/auth/recover/end"} {
		if result := client.call("GET", path, nil); result.status < 400 {
			test.Fatalf("GET %s = %d; authboss's form pages must stay unrouted", path, result.status)
		}
	}
}

func (client *session) cookie() *http.Cookie {
	serverURL, _ := url.Parse(client.server.URL)
	for _, cookie := range client.http.Jar.Cookies(serverURL) {
		if cookie.Name == auth.SessionCookie {
			return cookie
		}
	}
	return nil
}

// withCookie is another browser that holds a copy of cookie.
func (client *session) withCookie(cookie *http.Cookie) *session {
	serverURL, _ := url.Parse(client.server.URL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(serverURL, []*http.Cookie{{Name: cookie.Name, Value: cookie.Value}})
	return &session{test: client.test, server: client.server, http: &http.Client{Jar: jar}}
}

func TestLogoutEndsTheSessionOnTheServer(test *testing.T) {
	memory := testutil.NewMemory()
	client := newClient(test, memory, &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")

	other := newClient(test, memory, &testutil.Mailbox{})
	other.expect(other.call("POST", "/auth/login", credentials(email, password)), http.StatusOK, "")
	stolen := other.withCookie(other.cookie())
	stolen.expect(stolen.me(), http.StatusOK, "")
	other.expect(other.call("POST", "/auth/logout", nil), http.StatusOK, "")
	stolen.expect(stolen.me(), http.StatusUnauthorized, "unauthorized")

	client.expect(client.me(), http.StatusOK, "") // other sessions stay
	if len(memory.Sessions) != 1 {
		test.Fatalf("sessions = %d, want 1", len(memory.Sessions))
	}
}

func TestLoginStartsAFreshSession(test *testing.T) {
	client := newClient(test, testutil.NewMemory(), &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	first := client.cookie()
	client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusOK, "")
	second := client.cookie()
	if second == nil || second.Value == first.Value {
		test.Fatal("every login must issue a new token")
	}
	client.expect(client.withCookie(first).me(), http.StatusUnauthorized, "unauthorized") // the old one is gone
	client.expect(client.me(), http.StatusOK, "")
}

func TestRepeatedWrongPasswordsLockTheAccountUntilAReset(test *testing.T) {
	mailbox, memory := &testutil.Mailbox{}, testutil.NewMemory()
	elsewhere := newClient(test, memory, mailbox) // signed in on another device before the attack
	elsewhere.expect(elsewhere.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	client := newClient(test, memory, mailbox)

	for attempt := 1; attempt < 5; attempt++ {
		client.expect(client.call("POST", "/auth/login", credentials(email, "Wrong-Pass1")), http.StatusUnauthorized, "invalid_credentials")
	}
	client.expect(client.call("POST", "/auth/login", credentials(email, "Wrong-Pass1")), http.StatusTooManyRequests, "account_locked")
	client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusTooManyRequests, "account_locked")
	elsewhere.expect(elsewhere.me(), http.StatusOK, "")

	client.expect(client.call("POST", "/auth/recover", map[string]string{"email": email}), http.StatusOK, "")
	newPassword := "Fresh-Neg4tive"
	client.expect(client.call("POST", "/auth/recover/end", map[string]string{"token": mailbox.RecoverToken(), "password": newPassword}), http.StatusOK, "")
	elsewhere.expect(elsewhere.me(), http.StatusUnauthorized, "unauthorized") // a reset ends every session
	client.expect(client.call("POST", "/auth/login", credentials(email, newPassword)), http.StatusOK, "")
}
