package testutil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"meta-frames-server/internal/auth"
)

// TestPassword satisfies the account password rules.
const TestPassword = "Zone-System5"

// SignUp registers email through handler and returns its session cookie.
func SignUp(test *testing.T, handler http.Handler, email string) *http.Cookie {
	test.Helper()
	request := httptest.NewRequest("POST", "/auth/register", strings.NewReader(`{"email":"`+email+`","password":"`+TestPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == auth.SessionCookie && cookie.Value != "" {
			return cookie
		}
	}
	test.Fatalf("registering %s gave no session: status=%d body=%s", email, recorder.Code, recorder.Body)
	return nil
}

// SignedIn sends every request with cookie unless the request already carries one.
func SignedIn(handler http.Handler, cookie *http.Cookie) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if _, err := request.Cookie(cookie.Name); err != nil {
			request.AddCookie(cookie)
		}
		handler.ServeHTTP(w, request)
	})
}
