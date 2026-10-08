package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/aarondl/authboss/v3"

	"meta-frames-server/internal/api"
)

// Authboss answers failures through a Responder and finishes every successful action with a
// redirect. These implementations turn both into the API's JSON: an AuthResult on success and
// the usual {code, message} Problem on failure.

type responder struct{}

func (responder) Respond(w http.ResponseWriter, req *http.Request, _ int, page string, data authboss.HTMLData) error {
	if fields, ok := data[authboss.DataValidation].(map[string][]string); ok {
		status, code, message := validationProblem(page, fields)
		return writeJSON(w, status, api.Problem{Code: code, Message: message})
	}
	if message, ok := data[authboss.DataErr].(string); ok {
		// Only login reports DataErr: unknown email and wrong password look the same on purpose.
		return writeJSON(w, http.StatusUnauthorized, api.Problem{Code: "invalid_credentials", Message: message})
	}
	// The GET pages that render without an error are not routed (see Auth.Mount).
	return fmt.Errorf("auth: no JSON response for page %q at %s", page, req.URL.Path)
}

// validationProblem maps authboss's field errors to a status. Errors without a field are the
// module's own verdict: a taken email on register, a bad token on password reset.
func validationProblem(page string, fields map[string][]string) (status int, code, message string) {
	if general := fields[""]; len(general) > 0 {
		switch page {
		case "register":
			return http.StatusConflict, "email_taken", strings.Join(general, ", ")
		case "recover_end":
			return http.StatusUnprocessableEntity, "invalid_token", strings.Join(general, ", ")
		}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		text := strings.Join(fields[name], ", ")
		if name != "" {
			text = name + ": " + text
		}
		parts = append(parts, text)
	}
	return http.StatusBadRequest, "validation_failed", strings.Join(parts, "; ")
}

type redirector struct{ browser browserFlow }

func (redirector redirector) Redirect(w http.ResponseWriter, req *http.Request, options authboss.RedirectOptions) error {
	if isBrowserFlow(req) {
		return redirector.browser.redirect(w, req, options)
	}
	if options.Failure != "" {
		if req.URL.Path == "/login" { // the lock module refuses a locked account
			return writeJSON(w, http.StatusTooManyRequests, api.Problem{Code: "account_locked", Message: options.Failure})
		}
		return writeJSON(w, http.StatusUnauthorized, api.Problem{Code: "unauthorized", Message: options.Failure})
	}
	status, result := http.StatusOK, api.AuthResult{Message: options.Success}
	switch req.URL.Path {
	case "/register":
		status = http.StatusCreated
		result.User = userInRequest(req)
	case "/login":
		result.Message = "Logged in"
		result.User = userInRequest(req)
	}
	return writeJSON(w, status, result)
}

// userInRequest returns the user that authboss put in the request context after login or
// registration.
func userInRequest(req *http.Request) *api.User {
	user, ok := req.Context().Value(authboss.CTXKeyUser).(*User)
	if !ok {
		return nil
	}
	view := user.API()
	return &view
}

// errorHandler reports unexpected module errors (database down, broken mailer config) as a 500,
// and failed Google sign-ins as a redirect back to the front end.
type errorHandler struct{ browser browserFlow }

func (handler errorHandler) Wrap(serve func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		err := serve(w, req)
		if err == nil {
			return
		}
		if isBrowserFlow(req) {
			handler.browser.fail(w, req, err)
			return
		}
		slog.Error("auth request failed", "path", req.URL.Path, "err", err)
		_ = writeJSON(w, http.StatusInternalServerError, api.Problem{Code: "internal", Message: "internal error"})
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}
