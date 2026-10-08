package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/aarondl/authboss/v3"
)

// loginPage is the front-end page a failed Google sign-in returns to, with ?error=<code>.
const loginPage = "/login"

// browserFlow answers the Google sign-in routes. The browser itself walks through them (front
// end, Google, callback, front end), so every answer is a redirect instead of JSON.
type browserFlow struct{ appURL string }

func isBrowserFlow(req *http.Request) bool { return strings.HasPrefix(req.URL.Path, "/oauth2/") }

func (flow browserFlow) redirect(w http.ResponseWriter, req *http.Request, options authboss.RedirectOptions) error {
	if options.Failure != "" {
		code := "google_denied"
		if options.Failure == authboss.TxtLocked.Default {
			code = "account_locked"
		}
		flow.toLogin(w, req, code)
		return nil
	}
	if !strings.HasPrefix(req.URL.Path, "/oauth2/callback/") {
		// The start route sends the browser to Google; the URL comes from the provider config.
		http.Redirect(w, req, options.RedirectPath, http.StatusFound)
		return nil
	}
	http.Redirect(w, req, flow.appURL+safePath(options.RedirectPath), http.StatusFound)
	return nil
}

func (flow browserFlow) fail(w http.ResponseWriter, req *http.Request, err error) {
	code := "google_failed"
	switch {
	case errors.Is(err, ErrEmailNotVerified):
		code = "email_not_verified"
	case errors.Is(err, ErrIdentityConflict):
		code = "account_conflict"
	}
	slog.Warn("google sign-in failed", "path", req.URL.Path, "code", code, "err", err)
	flow.toLogin(w, req, code)
}

func (flow browserFlow) toLogin(w http.ResponseWriter, req *http.Request, code string) {
	http.Redirect(w, req, flow.appURL+loginPage+"?"+url.Values{"error": {code}}.Encode(), http.StatusFound)
}

// safePath keeps the after-sign-in destination (the ?redir= the front end passed to the start
// route) on the front end: only a path is accepted, never another site.
func safePath(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") ||
		strings.HasPrefix(target, "//") || strings.Contains(target, `\`) {
		return "/"
	}
	return target
}
