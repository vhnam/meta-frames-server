package auth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/server"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

// noQueries panics on any query (the embedded interface is nil), and the router's recovery turns
// that panic into a 500, so a page request that reads the database fails its status check.
type noQueries struct{ gen.Querier }

func verifyPageServer(test *testing.T) *httptest.Server {
	test.Helper()
	testServer := httptest.NewServer(verifyPageHandler(test))
	test.Cleanup(testServer.Close)
	return testServer
}

func verifyPageHandler(test *testing.T) http.Handler {
	test.Helper()
	store := testutil.Store{Querier: noQueries{}}
	accounts, err := auth.New(store, auth.Config{Mailer: &testutil.Mailbox{}})
	if err != nil {
		test.Fatal(err)
	}
	handler, err := server.NewHandler(controllers.New(services.New(store, testutil.NewMemoryFiles(), clock.Fixed{})), accounts, []string{"http://app.test"})
	if err != nil {
		test.Fatal(err)
	}
	return handler
}

func fetchVerifyPage(test *testing.T, testServer *httptest.Server, method, path string, withCookie bool) (*http.Response, string) {
	test.Helper()
	request, err := http.NewRequest(method, testServer.URL+path, nil)
	if err != nil {
		test.Fatal(err)
	}
	if withCookie {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "stale-session-token"})
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func TestVerifyPageHeadersAndCSPMatchTheServedContent(test *testing.T) {
	testServer := verifyPageServer(test)
	response, body := fetchVerifyPage(test, testServer, "GET", "/auth/verify-email", false)
	if response.StatusCode != http.StatusOK {
		test.Fatalf("status = %d", response.StatusCode)
	}
	for header, want := range map[string]string{
		"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer",
	} {
		if got := response.Header.Get(header); got != want {
			test.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	policies := response.Header.Values("Content-Security-Policy")
	if len(policies) != 1 {
		test.Fatalf("CSP header count = %d, want 1", len(policies))
	}
	policy := policies[0]
	script := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindStringSubmatch(body)
	style := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindStringSubmatch(body)
	if script == nil || style == nil {
		test.Fatal("body has no inline script or style")
	}
	if !strings.Contains(policy, "script-src "+hashOf(script[1])+";") || !strings.Contains(policy, "style-src "+hashOf(style[1])+";") {
		test.Errorf("CSP hashes do not match the served script and style: %s", policy)
	}
	if !strings.Contains(policy, "connect-src 'self'") || strings.Contains(policy, "unsafe-inline") {
		test.Errorf("CSP = %s", policy)
	}
}

func TestVerifyPageHeadMatchesGetWithoutBody(test *testing.T) {
	testServer := verifyPageServer(test)
	get, _ := fetchVerifyPage(test, testServer, "GET", "/auth/verify-email", false)
	head, body := fetchVerifyPage(test, testServer, "HEAD", "/auth/verify-email", false)
	if head.StatusCode != http.StatusOK || body != "" {
		test.Fatalf("HEAD status = %d, body length = %d", head.StatusCode, len(body))
	}
	for _, header := range []string{"Content-Type", "Cache-Control", "Referrer-Policy", "Content-Security-Policy"} {
		if head.Header.Get(header) != get.Header.Get(header) {
			test.Errorf("%s: HEAD %q, GET %q", header, head.Header.Get(header), get.Header.Get(header))
		}
	}
}

func TestVerifyPageHeadWritesNoBodyWithGzip(test *testing.T) {
	handler := verifyPageHandler(test)
	requestPage := func(method string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/auth/verify-email", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	get := requestPage(http.MethodGet)
	if get.Code != http.StatusOK || get.Header().Get("Content-Encoding") != "gzip" || get.Body.Len() == 0 {
		test.Fatal("GET did not serve a compressed page")
	}
	// The recorder sees writes that net/http would suppress for HEAD.
	head := requestPage(http.MethodHead)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		test.Fatalf("HEAD status = %d, body length = %d", head.Code, head.Body.Len())
	}
}

func TestVerifyPageMakesNoDatabaseCall(test *testing.T) {
	testServer := verifyPageServer(test)
	for _, method := range []string{"GET", "HEAD"} {
		for _, withCookie := range []bool{false, true} {
			response, _ := fetchVerifyPage(test, testServer, method, "/auth/verify-email?token=abc&x=1", withCookie)
			if response.StatusCode != http.StatusOK {
				test.Errorf("%s (cookie %v) status = %d, want 200", method, withCookie, response.StatusCode)
			}
		}
	}
}

func TestVerifyPageLoadsNothingExternal(test *testing.T) {
	_, body := fetchVerifyPage(test, verifyPageServer(test), "GET", "/auth/verify-email", false)
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") || strings.Contains(body, "src=") {
		test.Fatal("the page names an external resource")
	}
}
