package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestSecurityHeaders(test *testing.T) {
	handler := newAnonymousHandler(test)
	cases := []struct {
		name   string
		method string
		path   string
		origin string
	}{
		{name: "200 health", method: http.MethodGet, path: "/health"},
		{name: "401 session check", method: http.MethodGet, path: "/cameras"},
		{name: "403 CORS rejection", method: http.MethodPost, path: "/auth/logout", origin: "https://unlisted.example"},
		{name: "404 unknown route", method: http.MethodGet, path: "/nowhere"},
		{name: "204 CORS preflight", method: http.MethodOptions, path: "/cameras", origin: "http://localhost:5173"},
	}
	wantHeaders := map[string]string{
		"Strict-Transport-Security": "max-age=31536000",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Content-Security-Policy":   "frame-ancestors 'none'",
	}

	for _, scenario := range cases {
		test.Run(scenario.name, func(test *testing.T) {
			request := httptest.NewRequest(scenario.method, scenario.path, nil)
			if scenario.origin != "" {
				request.Header.Set("Origin", scenario.origin)
			}
			if scenario.method == http.MethodOptions {
				request.Header.Set("Access-Control-Request-Method", http.MethodPut)
			}
			headers := serve(handler, request).Result().Header
			for name, want := range wantHeaders {
				if got := headers.Values(name); !slices.Equal(got, []string{want}) {
					test.Errorf("%s = %q, want exactly %q", name, got, want)
				}
			}
		})
	}
}
