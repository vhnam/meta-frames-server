package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/server"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

const verifyLinkPrefix = "http://api.test/auth/verify-email#token="

type failingMailer struct{}

func (failingMailer) Send(context.Context, authboss.Email) error { return errors.New("smtp down") }

func verifyClient(test *testing.T, memory *testutil.Memory, mode auth.EmailVerificationMode, mailer authboss.Mailer) *session {
	test.Helper()
	return newClientWith(test, memory, auth.Config{
		EmailVerification: mode, AppURL: "http://app.test", APIURL: "http://api.test", MailFrom: "no-reply@app.test", Mailer: mailer,
	})
}

func TestRegistrationInSendModeStoresDigestsAndMailsTheToken(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)

	client.expect(client.call("POST", "/auth/register", credentials("Ansel@Example.com ", password)), http.StatusBadRequest, "validation_failed")
	registered := client.expect(client.call("POST", "/auth/register", credentials("Ansel@Example.com", password)), http.StatusCreated, "")
	if registered.body["user"].(map[string]any)["email"] != email {
		test.Fatalf("registration = %v", registered.body)
	}
	client.expect(client.me(), http.StatusOK, "") // the session cookie stays
	sent := mailbox.Sent()
	if len(sent) != 1 || len(sent[0].To) != 1 || sent[0].To[0] != email {
		test.Fatalf("mail = %+v, want one mail to %s", sent, email)
	}
	link := strings.Fields(sent[0].TextBody)
	var token string
	for _, word := range link {
		if rest, found := strings.CutPrefix(word, verifyLinkPrefix); found {
			token = rest
		}
	}
	if len(token) != 88 {
		test.Fatalf("token length = %d, want 88; body %q", len(token), sent[0].TextBody)
	}
	row := onlyUser(test, memory)
	if len(row.EmailVerificationSelector) != 64 || len(row.EmailVerificationVerifier) != 64 ||
		row.EmailVerificationIssuedCount != 1 || !row.EmailVerificationExpiresAt.Valid {
		test.Fatalf("challenge = %+v", row)
	}
	raw, err := base64.URLEncoding.DecodeString(token)
	generator := authboss.NewSha512TokenGenerator()
	if err != nil || len(raw) != generator.TokenSize() {
		test.Fatalf("invalid token encoding or size: length %d, error %v", len(raw), err)
	}
	selector, verifier := generator.ParseToken(string(raw))
	if !bytes.Equal(row.EmailVerificationSelector, selector) || !bytes.Equal(row.EmailVerificationVerifier, verifier) {
		test.Fatal("stored digests do not match the mailed token")
	}
}

func TestRegistrationVerificationLinkUsesTheVerificationPath(test *testing.T) {
	for _, scenario := range []struct{ name, root, path string }{
		{"query", "https://api.test/?tenant=film", "/auth/verify-email"},
		{"escaped prefix", "https://api.test/tenants/film%2F35mm/", "/tenants/film%2F35mm/auth/verify-email"},
		{"empty prefix segment", "https://api.test/tenants//film/", "/tenants//film/auth/verify-email"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			mailbox := &testutil.Mailbox{}
			client := newClientWith(test, testutil.NewMemory(), auth.Config{
				EmailVerification: auth.EmailVerificationSend,
				APIURL:            scenario.root,
				MailFrom:          "no-reply@app.test",
				Mailer:            mailbox,
			})
			client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
			sent := mailbox.Sent()
			if len(sent) != 1 {
				test.Fatalf("sent %d emails, want 1", len(sent))
			}
			for _, word := range strings.Fields(sent[0].TextBody) {
				if !strings.HasPrefix(word, "https://api.test/") {
					continue
				}
				link, err := url.Parse(word)
				if err != nil {
					test.Fatal("verification link is not a valid URL")
				}
				if path := link.EscapedPath(); path != scenario.path {
					test.Fatalf("verification link path = %q, want %q", path, scenario.path)
				}
				if link.RawQuery != "" || !strings.HasPrefix(link.Fragment, "token=") {
					test.Fatal("verification link must have a token fragment and no query")
				}
				return
			}
			test.Fatal("verification email has no link at the configured API host")
		})
	}
}

func TestRegistrationKeepsTheAccountWhenTheMailFails(test *testing.T) {
	memory := testutil.NewMemory()
	client := verifyClient(test, memory, auth.EmailVerificationSend, failingMailer{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	if row := onlyUser(test, memory); len(row.EmailVerificationSelector) != 64 || row.EmailVerificationIssuedCount != 1 {
		test.Fatalf("challenge = %+v", row)
	}
}

func TestRegistrationInOffModeIssuesNothing(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationOff, mailbox)
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	if row := onlyUser(test, memory); row.EmailVerificationSelector != nil || row.EmailVerificationIssuedCount != 0 || len(mailbox.Sent()) != 0 {
		test.Fatalf("off mode issued a challenge: %+v, mail %d", row, len(mailbox.Sent()))
	}
}

func TestDuplicateRegistrationInSendModeSendsNothingAndKeepsTheChallenge(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	before := onlyUser(test, memory)

	again := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	again.expect(again.call("POST", "/auth/register", credentials("ANSEL@example.com", password)), http.StatusConflict, "email_taken")
	after := onlyUser(test, memory)
	if len(mailbox.Sent()) != 1 || string(after.EmailVerificationSelector) != string(before.EmailVerificationSelector) ||
		after.EmailVerificationIssuedCount != 1 {
		test.Fatalf("duplicate changed the challenge or sent mail: %+v, mail %d", after, len(mailbox.Sent()))
	}
}

func assertGoogleProof(test *testing.T, memory *testutil.Memory, mailbox *testutil.Mailbox) {
	test.Helper()
	row := onlyUser(test, memory)
	if row.EmailVerificationSource == nil || *row.EmailVerificationSource != "google" || !row.EmailVerifiedAt.Valid ||
		row.EmailVerificationSelector != nil || len(mailbox.Sent()) != 0 {
		test.Fatalf("google account = %+v, mail %d", row, len(mailbox.Sent()))
	}
}

func TestNewGoogleAccountRecordsProofInEveryMode(test *testing.T) {
	for _, mode := range []auth.EmailVerificationMode{"", auth.EmailVerificationOff, auth.EmailVerificationSend, auth.EmailVerificationEnforce} {
		test.Run("callback "+string(mode), func(test *testing.T) {
			google, memory, mailbox := newFakeGoogle(test), testutil.NewMemory(), &testutil.Mailbox{}
			client := newClientWith(test, memory, auth.Config{
				EmailVerification: mode, AppURL: "http://app.test", APIURL: "http://api.test", Mailer: mailbox, Google: google.Config(),
			})
			client.signInWithGoogle("/")
			assertGoogleProof(test, memory, mailbox)
		})
		test.Run("token "+string(mode), func(test *testing.T) {
			memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
			client := newClientWith(test, memory, auth.Config{
				EmailVerification: mode, AppURL: "http://app.test", Mailer: mailbox,
				Google: auth.GoogleConfig{
					MobileClientIDs: []string{mobileClient},
					VerifyIDToken:   func(context.Context, string) (auth.GoogleIDClaims, error) { return tokenClaims(), nil },
				},
			})
			client.expect(client.signInWithToken(goodToken), http.StatusOK, "")
			assertGoogleProof(test, memory, mailbox)
		})
	}
}

func TestReturningGoogleSubjectGetsNoProofAndKeepsItsEmail(test *testing.T) {
	google, memory := newFakeGoogle(test), testutil.NewMemory()
	provider, subject, stored := "google", "g-1", "old@example.com"
	id := uuid.New()
	memory.Users[id] = gen.AppUser{ID: id, Email: stored, Oauth2Provider: &provider, Oauth2Uid: &subject}
	client := newClientWith(test, memory, auth.Config{
		EmailVerification: auth.EmailVerificationSend, AppURL: "http://app.test", APIURL: "http://api.test",
		Mailer: &testutil.Mailbox{}, Google: google.Config(),
	})
	client.signInWithGoogle("/")
	if row := onlyUser(test, memory); row.EmailVerifiedAt.Valid || row.EmailVerificationSource != nil || row.Email != stored {
		test.Fatalf("returning account = %+v", row)
	}
}

type verifyReply struct {
	status     int
	code       string
	retryAfter string
	setCookie  bool
}

// postVerify sends POST /auth/verify-email and keeps the headers that the tests check.
func postVerify(test *testing.T, client *session, token string, password *string) verifyReply {
	test.Helper()
	body := map[string]any{"token": token}
	if password != nil {
		body["password"] = *password
	}
	payload, _ := json.Marshal(body)
	request, _ := http.NewRequest("POST", client.server.URL+"/auth/verify-email", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var decoded struct{ Code string }
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return verifyReply{response.StatusCode, decoded.Code, response.Header.Get("Retry-After"), len(response.Header.Values("Set-Cookie")) > 0}
}

func (reply verifyReply) expect(test *testing.T, status int, code string) {
	test.Helper()
	if reply.status != status || reply.code != code {
		test.Fatalf("verify = %d %q, want %d %q", reply.status, reply.code, status, code)
	}
}

// registeredToken registers the test account in send mode and returns the token from the mailed link.
func registeredToken(test *testing.T, client *session, mailbox *testutil.Mailbox) string {
	test.Helper()
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	sent := mailbox.Sent()
	if len(sent) != 1 {
		test.Fatalf("sent %d emails, want 1", len(sent))
	}
	for _, word := range strings.Fields(sent[0].TextBody) {
		if token, found := strings.CutPrefix(word, verifyLinkPrefix); found {
			return token
		}
	}
	test.Fatal("no verification link in the mail")
	return ""
}

func verifyFixture(test *testing.T) (*session, *testutil.Memory, string) {
	test.Helper()
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	return client, memory, registeredToken(test, client, mailbox)
}

func pointer(value string) *string { return &value }

// changeToken returns the token with one byte of the chosen half flipped.
func changeToken(test *testing.T, token string, index int) string {
	test.Helper()
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		test.Fatal(err)
	}
	raw[index] ^= 0xff
	return base64.URLEncoding.EncodeToString(raw)
}

func TestVerifyWithTheRightPasswordRecordsProofAndTouchesNoSession(test *testing.T) {
	client, memory, token := verifyFixture(test)
	reply := postVerify(test, client, token, pointer(password))
	reply.expect(test, http.StatusOK, "")
	row := onlyUser(test, memory)
	if reply.setCookie {
		test.Fatal("verify answered with a cookie")
	}
	if row.EmailVerificationSource == nil || *row.EmailVerificationSource != "link" || !row.EmailVerifiedAt.Valid ||
		row.EmailVerificationSelector != nil || row.EmailVerificationVerifier != nil || row.EmailVerificationExpiresAt.Valid {
		test.Fatalf("row after verify = %+v", row)
	}
	client.expect(client.me(), http.StatusOK, "") // the registration session still works
}

func TestVerifyNeedsTheAccountPassword(test *testing.T) {
	for name, given := range map[string]*string{"missing": nil, "wrong": pointer("Wrong-Pass9")} {
		test.Run(name, func(test *testing.T) {
			client, memory, token := verifyFixture(test)
			before := onlyUser(test, memory)
			postVerify(test, client, token, given).expect(test, http.StatusUnprocessableEntity, "invalid_token")
			if after := onlyUser(test, memory); !reflect.DeepEqual(before, after) {
				test.Fatalf("a refused password changed the row: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestVerifyAGoogleOnlyRowNeedsNoPassword(test *testing.T) {
	memory := testutil.NewMemory()
	client := verifyClient(test, memory, auth.EmailVerificationSend, &testutil.Mailbox{})
	selector, verifier, token, err := authboss.NewSha512TokenGenerator().GenerateToken()
	if err != nil {
		test.Fatal(err)
	}
	selectorBytes, _ := base64.StdEncoding.DecodeString(selector)
	verifierBytes, _ := base64.StdEncoding.DecodeString(verifier)
	provider, subject, id := "google", "g-1", uuid.New()
	memory.Users[id] = gen.AppUser{
		ID: id, Email: "legacy@example.com", Oauth2Provider: &provider, Oauth2Uid: &subject,
		EmailVerificationSelector: selectorBytes, EmailVerificationVerifier: verifierBytes,
		EmailVerificationExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		EmailVerificationExempt:    true,
	}
	postVerify(test, client, token, pointer("ignored")).expect(test, http.StatusOK, "")
	row := onlyUser(test, memory)
	if !row.EmailVerifiedAt.Valid || row.EmailVerificationExempt || *row.EmailVerificationSource != "link" ||
		row.Oauth2Uid == nil || *row.Oauth2Uid != subject || *row.Oauth2Provider != provider {
		test.Fatalf("legacy row = %+v", row)
	}
}

func TestVerifyRefusesBadTokens(test *testing.T) {
	client, memory, token := verifyFixture(test)
	for _, bad := range []string{"abc", strings.Repeat("!", 88), strings.Repeat("A", 88)} { // length, characters, padding
		postVerify(test, client, bad, nil).expect(test, http.StatusBadRequest, "bad_request")
	}
	postVerify(test, client, changeToken(test, token, 5), pointer(password)).expect(test, http.StatusUnprocessableEntity, "invalid_token")  // unknown selector
	postVerify(test, client, changeToken(test, token, 40), pointer(password)).expect(test, http.StatusUnprocessableEntity, "invalid_token") // wrong verifier
	if row := onlyUser(test, memory); row.EmailVerifiedAt.Valid || row.EmailVerificationSelector == nil {
		test.Fatalf("a bad token changed the row: %+v", row)
	}
}

func TestVerifyTokenWorksOnce(test *testing.T) {
	client, _, token := verifyFixture(test)
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusOK, "")
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusUnprocessableEntity, "invalid_token")
}

func TestVerifyRefusesALockedAccountAndKeepsTheChallenge(test *testing.T) {
	client, memory, token := verifyFixture(test)
	for id, user := range memory.Users {
		user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		memory.Users[id] = user
	}
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusUnprocessableEntity, "invalid_token")
	if row := onlyUser(test, memory); row.EmailVerifiedAt.Valid || row.EmailVerificationSelector == nil {
		test.Fatalf("a locked account was verified or lost its challenge: %+v", row)
	}
}

func TestVerifyStopsAfterTenPasswordAttemptsOnOneChallenge(test *testing.T) {
	client, memory, token := verifyFixture(test)
	for range 10 {
		postVerify(test, client, token, pointer("Wrong-Pass9")).expect(test, http.StatusUnprocessableEntity, "invalid_token")
	}
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusUnprocessableEntity, "invalid_token")
	if row := onlyUser(test, memory); row.EmailVerifiedAt.Valid {
		test.Fatal("the 11th attempt verified the account")
	}
}

func TestVerifyLimitsOneSource(test *testing.T) {
	client, _, token := verifyFixture(test)
	unknown := changeToken(test, token, 5)
	for range 30 {
		postVerify(test, client, unknown, nil).expect(test, http.StatusUnprocessableEntity, "invalid_token")
	}
	reply := postVerify(test, client, unknown, nil)
	reply.expect(test, http.StatusTooManyRequests, "rate_limited")
	if seconds, err := strconv.Atoi(reply.retryAfter); err != nil || seconds < 1 {
		test.Fatalf("Retry-After = %q, want positive seconds", reply.retryAfter)
	}
}

func TestVerifyRefusesANoncanonicalTokenSpelling(test *testing.T) {
	client, memory, token := verifyFixture(test)
	// The last data character holds 2 bits of the token and 4 padding bits that must be zero.
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	index := strings.IndexByte(alphabet, token[85])
	if index < 0 || index%16 != 0 {
		test.Fatalf("canonical token ends with a nonzero padding bits character %q", token[85])
	}
	spelled := token[:85] + string(alphabet[index+1]) + token[86:]
	postVerify(test, client, spelled, pointer(password)).expect(test, http.StatusBadRequest, "bad_request")
	if row := onlyUser(test, memory); row.EmailVerifiedAt.Valid {
		test.Fatal("a noncanonical spelling verified the account")
	}
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusOK, "")
}

// sessionsFailing is a Memory whose session lookup fails once switched on.
type sessionsFailing struct {
	*testutil.Memory
	fail bool
}

func (querier *sessionsFailing) GetSessionUser(ctx context.Context, tokenHash []byte) (gen.AppUser, error) {
	if querier.fail {
		return gen.AppUser{}, errors.New("session store down")
	}
	return querier.Memory.GetSessionUser(ctx, tokenHash)
}

func TestVerifyReadsNoSessionButOtherRoutesDo(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	querier := &sessionsFailing{Memory: memory}
	store := testutil.Store{Querier: querier}
	accounts, err := auth.New(store, auth.Config{
		EmailVerification: auth.EmailVerificationSend, AppURL: "http://app.test", APIURL: "http://api.test", MailFrom: "no-reply@app.test", Mailer: mailbox,
	})
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
	client := &session{test: test, server: testServer, http: &http.Client{Jar: jar}}
	token := registeredToken(test, client, mailbox) // the client now holds a session cookie

	querier.fail = true
	client.expect(client.me(), http.StatusInternalServerError, "internal") // a protected route still loads the session
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusOK, "")
}

type resendReply struct {
	status int
	body   string
	header http.Header
}

// postResend sends POST /auth/verify-email/resend with a raw body. The header copy drops the two
// values that differ on every request.
func postResend(test *testing.T, client *session, rawBody string) resendReply {
	test.Helper()
	request, _ := http.NewRequest("POST", client.server.URL+"/auth/verify-email/resend", strings.NewReader(rawBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.http.Do(request)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body bytes.Buffer
	_, _ = body.ReadFrom(response.Body)
	header := response.Header.Clone()
	header.Del("X-Request-ID")
	header.Del("Date")
	return resendReply{response.StatusCode, body.String(), header}
}

func resendFor(test *testing.T, client *session, address string) resendReply {
	test.Helper()
	payload, _ := json.Marshal(map[string]string{"email": address})
	return postResend(test, client, string(payload))
}

// changeOnlyUser applies change to the only account row.
func changeOnlyUser(test *testing.T, memory *testutil.Memory, change func(*gen.AppUser)) {
	test.Helper()
	for id, user := range memory.Users {
		change(&user)
		memory.Users[id] = user
	}
	onlyUser(test, memory)
}

// pastCooldown moves the last issue 2 minutes back, so the 60-second cooldown is over.
func pastCooldown(user *gen.AppUser) {
	user.EmailVerificationLastIssuedAt = pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Minute), Valid: true}
}

func tokenIn(test *testing.T, sent authboss.Email) string {
	test.Helper()
	for _, word := range strings.Fields(sent.TextBody) {
		if token, found := strings.CutPrefix(word, verifyLinkPrefix); found {
			return token
		}
	}
	test.Fatal("no verification link in the mail")
	return ""
}

func TestResendReplacesTheChallengeAndTheOldTokenStopsWorking(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	oldToken := registeredToken(test, client, mailbox)
	changeOnlyUser(test, memory, pastCooldown)

	if reply := resendFor(test, client, " Ansel@Example.com "); reply.status != http.StatusAccepted {
		test.Fatalf("resend = %d %s, want 202", reply.status, reply.body)
	}
	sent := mailbox.Sent()
	if len(sent) != 2 || len(sent[1].To) != 1 || sent[1].To[0] != email {
		test.Fatalf("mail = %+v, want a second mail to %s", sent, email)
	}
	newToken := tokenIn(test, sent[1])
	postVerify(test, client, oldToken, pointer(password)).expect(test, http.StatusUnprocessableEntity, "invalid_token")
	postVerify(test, client, newToken, pointer(password)).expect(test, http.StatusOK, "")
}

func TestResendInsideTheCooldownSendsNothingAndKeepsTheChallenge(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	token := registeredToken(test, client, mailbox)
	before := onlyUser(test, memory)

	if reply := resendFor(test, client, email); reply.status != http.StatusAccepted {
		test.Fatalf("resend = %d, want 202", reply.status)
	}
	if after := onlyUser(test, memory); len(mailbox.Sent()) != 1 || !reflect.DeepEqual(before, after) {
		test.Fatalf("cooldown resend changed state: mail %d, before %+v, after %+v", len(mailbox.Sent()), before, after)
	}
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusOK, "")
}

func TestResendStopsAfterFiveIssuesInADay(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	registeredToken(test, client, mailbox)
	changeOnlyUser(test, memory, func(user *gen.AppUser) {
		pastCooldown(user)
		user.EmailVerificationIssuedCount = 5
		user.EmailVerificationWindowStartedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	})
	before := onlyUser(test, memory)
	resendFor(test, client, email)
	if after := onlyUser(test, memory); len(mailbox.Sent()) != 1 || !reflect.DeepEqual(before, after) {
		test.Fatalf("the sixth issue in 24 hours changed state: mail %d, after %+v", len(mailbox.Sent()), after)
	}
}

func TestResendSendsNothingForUnknownVerifiedAndLockedRows(test *testing.T) {
	for name, change := range map[string]func(*gen.AppUser){
		"verified": func(user *gen.AppUser) { user.EmailVerifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true} },
		"locked": func(user *gen.AppUser) {
			user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		},
		"unknown": func(user *gen.AppUser) { user.Email = "someone-else@example.com" },
	} {
		test.Run(name, func(test *testing.T) {
			memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
			client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
			registeredToken(test, client, mailbox)
			changeOnlyUser(test, memory, func(user *gen.AppUser) { pastCooldown(user); change(user) })
			before := onlyUser(test, memory)
			if reply := resendFor(test, client, email); reply.status != http.StatusAccepted {
				test.Fatalf("resend = %d, want 202", reply.status)
			}
			if after := onlyUser(test, memory); len(mailbox.Sent()) != 1 || !reflect.DeepEqual(before, after) {
				test.Fatalf("%s row changed state: mail %d, after %+v", name, len(mailbox.Sent()), after)
			}
		})
	}
}

func TestResendMailsAnExemptUnverifiedRow(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	id, provider, subject := uuid.New(), "google", "g-1"
	memory.Users[id] = gen.AppUser{ID: id, Email: "legacy@example.com", Oauth2Provider: &provider, Oauth2Uid: &subject, EmailVerificationExempt: true}
	if reply := resendFor(test, client, "legacy@example.com"); reply.status != http.StatusAccepted {
		test.Fatalf("resend = %d, want 202", reply.status)
	}
	if sent := mailbox.Sent(); len(sent) != 1 || sent[0].To[0] != "legacy@example.com" {
		test.Fatalf("mail = %+v, want one mail to the exempt row", sent)
	}
	if row := onlyUser(test, memory); row.EmailVerificationSelector == nil || !row.EmailVerificationExempt {
		test.Fatalf("exempt row = %+v", row)
	}
}

func TestResendAnswersAllPathsTheSame(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	registeredToken(test, client, mailbox)
	changeOnlyUser(test, memory, pastCooldown)

	eligible := resendFor(test, client, email)
	cooldown := resendFor(test, client, email)
	changeOnlyUser(test, memory, func(user *gen.AppUser) {
		pastCooldown(user)
		user.EmailVerificationIssuedCount = 5
		user.EmailVerificationWindowStartedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	})
	windowLimited := resendFor(test, client, email)
	changeOnlyUser(test, memory, func(user *gen.AppUser) {
		user.EmailVerificationIssuedCount = 2
	})
	unknown := resendFor(test, client, "nobody@example.com")
	changeOnlyUser(test, memory, func(user *gen.AppUser) {
		user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	})
	locked := resendFor(test, client, email)
	changeOnlyUser(test, memory, func(user *gen.AppUser) {
		user.LockedUntil = pgtype.Timestamptz{}
		user.EmailVerifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	})
	verified := resendFor(test, client, email)

	if eligible.status != http.StatusAccepted || len(mailbox.Sent()) != 2 {
		test.Fatalf("eligible = %d, mail %d, want 202 and a second mail", eligible.status, len(mailbox.Sent()))
	}
	for name, other := range map[string]resendReply{"cooldown": cooldown, "window": windowLimited, "unknown": unknown, "locked": locked, "verified": verified} {
		if !reflect.DeepEqual(eligible, other) {
			test.Fatalf("%s answer differs from the eligible answer: %+v vs %+v", name, other, eligible)
		}
	}
	if eligible.header.Get("Retry-After") != "" || len(eligible.header.Values("Set-Cookie")) != 0 {
		test.Fatalf("202 headers = %v, want no Retry-After and no cookie", eligible.header)
	}
}

func TestResendInOffModeAnswers202AndChangesNothing(test *testing.T) {
	for name, mode := range map[string]auth.EmailVerificationMode{"explicit off": auth.EmailVerificationOff, "zero value": ""} {
		test.Run(name, func(test *testing.T) {
			memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
			client := verifyClient(test, memory, mode, mailbox)
			client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
			before := onlyUser(test, memory)
			if reply := resendFor(test, client, email); reply.status != http.StatusAccepted {
				test.Fatalf("resend = %d, want 202", reply.status)
			}
			if after := onlyUser(test, memory); len(mailbox.Sent()) != 0 || !reflect.DeepEqual(before, after) {
				test.Errorf("resend in off mode sent %d messages or changed the account", len(mailbox.Sent()))
			}
		})
	}
}

func TestResendAfterVerifyIssuesNothing(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	token := registeredToken(test, client, mailbox)
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusOK, "")
	changeOnlyUser(test, memory, pastCooldown)
	resendFor(test, client, email)
	if row := onlyUser(test, memory); len(mailbox.Sent()) != 1 || row.EmailVerificationSelector != nil {
		test.Fatalf("resend after proof issued a challenge: mail %d, row %+v", len(mailbox.Sent()), row)
	}
}

// blockingMailer holds every send until release is closed. started gets one value per send.
type blockingMailer struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
}

func newBlockingMailer() *blockingMailer {
	return &blockingMailer{release: make(chan struct{}), started: make(chan struct{}, 64)}
}

func (mailer *blockingMailer) Send(context.Context, authboss.Email) error {
	mailer.started <- struct{}{}
	<-mailer.release
	return nil
}

func (mailer *blockingMailer) free() { mailer.once.Do(func() { close(mailer.release) }) }

// waitForFreeMailSlots polls until every package mail slot is free. It takes no slot itself.
func waitForFreeMailSlots(test *testing.T) {
	test.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for auth.FreeMailSlots() != 8 {
		if time.Now().After(deadline) {
			test.Error("mail slots were not freed within 5 seconds")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResendDoesNotWaitForTheMailer(test *testing.T) {
	blocker := newBlockingMailer()
	// Cleanups run last-in first-out: free the mailer, then close the server (it waits for open
	// requests), then check that every shared mailSlots slot came back.
	test.Cleanup(func() { waitForFreeMailSlots(test) })
	memory := testutil.NewMemory()
	client := verifyClient(test, memory, auth.EmailVerificationSend, auth.AsyncMailer{Mailer: blocker})
	test.Cleanup(blocker.free)

	// post runs in a goroutine, so it returns its error instead of calling test.Fatal.
	type outcome struct {
		status int
		err    error
	}
	post := func(path string, body any) <-chan outcome {
		done := make(chan outcome, 1)
		payload, _ := json.Marshal(body)
		go func() {
			response, err := client.http.Post(client.server.URL+path, "application/json", bytes.NewReader(payload))
			if err != nil {
				done <- outcome{err: err}
				return
			}
			_ = response.Body.Close()
			done <- outcome{status: response.StatusCode}
		}()
		return done
	}
	answers := []struct {
		path   string
		body   any
		status int
	}{
		{"/auth/register", credentials(email, password), http.StatusCreated},
		{"/auth/verify-email/resend", map[string]string{"email": email}, http.StatusAccepted},
	}
	for index, answer := range answers {
		if index == 1 {
			changeOnlyUser(test, memory, pastCooldown)
		}
		select {
		case result := <-post(answer.path, answer.body):
			if result.err != nil || result.status != answer.status {
				test.Fatalf("POST %s = %d, %v, want %d", answer.path, result.status, result.err, answer.status)
			}
		case <-time.After(5 * time.Second):
			test.Fatalf("POST %s waited for the blocked mailer", answer.path)
		}
	}
	for range 2 { // the registration mail and the resend mail both reach the blocked mailer
		select {
		case <-blocker.started:
		case <-time.After(5 * time.Second):
			test.Fatal("a send never reached the blocked mailer")
		}
	}
}

func TestResendLimitsOneSource(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	for range 30 {
		if reply := resendFor(test, client, "nobody@example.com"); reply.status != http.StatusAccepted {
			test.Fatalf("resend = %d, want 202", reply.status)
		}
	}
	reply := resendFor(test, client, "someone-else@example.com")
	if seconds, err := strconv.Atoi(reply.header.Get("Retry-After")); reply.status != http.StatusTooManyRequests || err != nil || seconds < 1 {
		test.Fatalf("31st resend = %d, Retry-After %q, want 429 with positive seconds", reply.status, reply.header.Get("Retry-After"))
	}
}

func TestResendRefusesABadBody(test *testing.T) {
	client := verifyClient(test, testutil.NewMemory(), auth.EmailVerificationSend, &testutil.Mailbox{})
	long := strings.Repeat("a", 250) + "@example.com"
	for name, body := range map[string]string{
		"malformed json": `{"email":`,
		"missing":        `{}`,
		"empty":          `{"email":"  "}`,
		"invalid":        `{"email":"not-an-address"}`,
		"display name":   `{"email":"Ansel <ansel@example.com>"}`,
		"too long":       `{"email":"` + long + `"}`,
		"extra field":    `{"email":"ansel@example.com","token":"x"}`,
	} {
		reply := postResend(test, client, body)
		var problem api.Problem
		if err := json.Unmarshal([]byte(reply.body), &problem); err != nil {
			test.Fatalf("%s: decode problem response: %v", name, err)
		}
		if reply.status != http.StatusBadRequest || problem.Code != "bad_request" || reply.header.Get("Retry-After") != "" {
			test.Fatalf("%s = %d %q with Retry-After %q, want 400 bad_request without Retry-After", name, reply.status, problem.Code, reply.header.Get("Retry-After"))
		}
	}
}

// issueFailing is a Memory whose challenge write fails.
type issueFailing struct{ *testutil.Memory }

func (issueFailing) IssueEmailVerification(context.Context, gen.IssueEmailVerificationParams) (string, error) {
	return "", errors.New("database down")
}

func TestResendAnswers500WhenTheDatabaseFails(test *testing.T) {
	mailbox := &testutil.Mailbox{}
	store := testutil.Store{Querier: issueFailing{testutil.NewMemory()}}
	accounts, err := auth.New(store, auth.Config{
		EmailVerification: auth.EmailVerificationSend, AppURL: "http://app.test", APIURL: "http://api.test", MailFrom: "no-reply@app.test", Mailer: mailbox,
	})
	if err != nil {
		test.Fatal(err)
	}
	handler, err := server.NewHandler(controllers.New(services.New(store, testutil.NewMemoryFiles(), clock.Fixed{})), accounts, []string{"http://app.test"})
	if err != nil {
		test.Fatal(err)
	}
	testServer := httptest.NewServer(handler)
	test.Cleanup(testServer.Close)
	client := &session{test: test, server: testServer, http: &http.Client{}}
	if reply := resendFor(test, client, email); reply.status != http.StatusInternalServerError || len(mailbox.Sent()) != 0 {
		test.Fatalf("resend = %d, mail %d, want 500 and no mail", reply.status, len(mailbox.Sent()))
	}
}

func TestResendCountsTheAddressLengthInCharacters(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	client := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	// 142 characters, 272 bytes: inside the 254-character limit, over 254 bytes.
	address := strings.Repeat("é", 130) + "@example.com"
	if reply := resendFor(test, client, address); reply.status != http.StatusAccepted {
		test.Fatalf("a %d-character address gave %d %s, want 202", len([]rune(address)), reply.status, reply.body)
	}
}

func TestEnforceRegistrationStartsNoSession(test *testing.T) {
	for name, mailer := range map[string]authboss.Mailer{"mail works": &testutil.Mailbox{}, "mail fails": failingMailer{}} {
		test.Run(name, func(test *testing.T) {
			memory := testutil.NewMemory()
			client := verifyClient(test, memory, auth.EmailVerificationEnforce, mailer)
			registered := client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
			if registered.body["verificationRequired"] != true {
				test.Fatalf("register body = %v, want verificationRequired", registered.body)
			}
			if _, leaked := registered.body["user"]; leaked {
				test.Fatal("register must not return the user before proof")
			}
			if client.cookie() != nil {
				test.Fatal("register started a session")
			}
			if len(memory.Sessions) != 0 {
				test.Fatal("register inserted a session row before proof")
			}
			client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
			if row := onlyUser(test, memory); len(row.EmailVerificationSelector) != 64 {
				test.Fatalf("challenge = %+v", row)
			}
			if mailbox, ok := mailer.(*testutil.Mailbox); ok && len(mailbox.Sent()) != 1 {
				test.Fatalf("sent %d emails, want 1", len(mailbox.Sent()))
			}
		})
	}
}

func TestEnforceLoginRefusesAnUnverifiedAccountWithoutCountingAnAttempt(test *testing.T) {
	memory := testutil.NewMemory()
	client := verifyClient(test, memory, auth.EmailVerificationEnforce, &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")

	client.expect(client.call("POST", "/auth/login", credentials(email, "Wrong-Pass1")), http.StatusUnauthorized, "invalid_credentials")
	before := onlyUser(test, memory)
	client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusForbidden, "email_verification_required")
	if client.cookie() != nil {
		test.Fatal("the refused login started a session")
	}
	if after := onlyUser(test, memory); after.AttemptCount != before.AttemptCount || after.AttemptCount == 0 {
		test.Fatalf("attempt count %d then %d, want unchanged and counted from the wrong password", before.AttemptCount, after.AttemptCount)
	}
}

func TestEnforceLockedAccountAnswers429BeforeTheVerificationRefusal(test *testing.T) {
	memory := testutil.NewMemory()
	client := verifyClient(test, memory, auth.EmailVerificationEnforce, &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	for attempt := 0; attempt < 5; attempt++ {
		client.call("POST", "/auth/login", credentials(email, "Wrong-Pass1"))
	}
	client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusTooManyRequests, "account_locked")
}

func TestEnforceLoginAcceptsVerifiedAndExemptAccounts(test *testing.T) {
	proof := func(user *gen.AppUser) { user.EmailVerifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true} }
	exempt := func(user *gen.AppUser) { user.EmailVerificationExempt = true }
	for name, change := range map[string]func(*gen.AppUser){"verified": proof, "exempt": exempt} {
		test.Run(name, func(test *testing.T) {
			memory := testutil.NewMemory()
			verifyClient(test, memory, auth.EmailVerificationSend, &testutil.Mailbox{}).
				call("POST", "/auth/register", credentials(email, password))
			changeOnlyUser(test, memory, change)
			client := verifyClient(test, memory, auth.EmailVerificationEnforce, &testutil.Mailbox{})
			client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusOK, "")
			if client.cookie() == nil {
				test.Fatal("login set no session cookie")
			}
			client.expect(client.me(), http.StatusOK, "")
		})
	}
}

func TestSendModeKeepsAnUnverifiedAccountSignedIn(test *testing.T) {
	client := verifyClient(test, testutil.NewMemory(), auth.EmailVerificationSend, &testutil.Mailbox{})
	client.expect(client.call("POST", "/auth/register", credentials(email, password)), http.StatusCreated, "")
	client.expect(client.me(), http.StatusOK, "")
	client.expect(client.call("POST", "/auth/login", credentials(email, password)), http.StatusOK, "")
}

func TestEnforceTurnsAnExistingUnverifiedSessionAnonymousUntilProof(test *testing.T) {
	memory, mailbox := testutil.NewMemory(), &testutil.Mailbox{}
	sendClient := verifyClient(test, memory, auth.EmailVerificationSend, mailbox)
	token := registeredToken(test, sendClient, mailbox)
	cookie := sendClient.cookie()

	client := verifyClient(test, memory, auth.EmailVerificationEnforce, mailbox).withCookie(cookie)
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	client.expect(client.call("POST", "/auth/logout", nil), http.StatusOK, "")
	if len(memory.Sessions) != 1 {
		test.Fatal("anonymous logout changed the blocked account's session rows")
	}
	client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	postVerify(test, client, token, pointer(password)).expect(test, http.StatusOK, "")
	client.expect(client.me(), http.StatusOK, "") // the session row was kept
}

func TestEnforceGoogleSignIn(test *testing.T) {
	googleClient := func(test *testing.T, google *fakeGoogle, memory *testutil.Memory) *session {
		return newClientWith(test, memory, auth.Config{
			EmailVerification: auth.EmailVerificationEnforce, AppURL: "http://app.test", APIURL: "http://api.test",
			Mailer: &testutil.Mailbox{}, Google: google.Config(),
		})
	}
	test.Run("new subject", func(test *testing.T) {
		client := googleClient(test, newFakeGoogle(test), testutil.NewMemory())
		client.signInWithGoogle("/")
		client.expect(client.me(), http.StatusOK, "")
	})
	test.Run("returning exempt subject", func(test *testing.T) {
		memory := testutil.NewMemory()
		provider, subject, id := "google", "g-1", uuid.New()
		memory.Users[id] = gen.AppUser{ID: id, Email: email, Oauth2Provider: &provider, Oauth2Uid: &subject, EmailVerificationExempt: true}
		client := googleClient(test, newFakeGoogle(test), memory)
		client.signInWithGoogle("/")
		client.expect(client.me(), http.StatusOK, "")
	})
	test.Run("new subject on a verified password account", func(test *testing.T) {
		memory := testutil.NewMemory()
		verifyClient(test, memory, auth.EmailVerificationSend, &testutil.Mailbox{}).
			call("POST", "/auth/register", credentials(email, password))
		changeOnlyUser(test, memory, func(user *gen.AppUser) { user.EmailVerifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true} })
		client := googleClient(test, newFakeGoogle(test), memory)
		if landing := client.signInWithGoogle("/"); landing != "http://app.test/login?error=account_conflict" {
			test.Fatalf("landed on %q, want account_conflict", landing)
		}
		client.expect(client.me(), http.StatusUnauthorized, "unauthorized")
	})
}
