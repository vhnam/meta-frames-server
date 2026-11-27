package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aarondl/authboss/v3"
	"github.com/gin-gonic/gin"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
)

// verifyEmailRoute completes the email verification. The token travels in the body, never in a URL.
const verifyEmailRoute = "/verify-email"

// resendEmailRoute asks for a new verification link.
const resendEmailRoute = "/verify-email/resend"

// maxEmailLength is the longest address the API accepts.
const maxEmailLength = 254

// dummyVerifier is compared when no challenge matches, so a miss costs about what a hit costs.
var dummyVerifier = make([]byte, 64)

var errInvalidToken = errors.New("auth: the verification token no longer works")

// sendVerification mails the verification link to a new password account. The raw token lives only
// in the user value that Create filled in. A send error never fails the registration: the account
// can ask for a new link later. It returns handled = false, so registration answers as before.
func (accounts *Auth) sendVerification(_ http.ResponseWriter, request *http.Request, _ bool) (bool, error) {
	user, ok := request.Context().Value(authboss.CTXKeyUser).(*User)
	if !ok || user.verificationToken == "" {
		return false, nil
	}
	token := user.verificationToken
	user.verificationToken = ""
	if err := mailVerification(request.Context(), &accounts.authboss.Config, user.Email, token); err != nil {
		// No recipient, token or link: the error text can echo them.
		slog.Error("sending the verification email failed")
	}
	return false, nil
}

// recordPasswordHash hands the password hash that the login just checked to the session store, which
// starts the session only while the account still has it. It runs after the lock module's handler.
func (accounts *Auth) recordPasswordHash(w http.ResponseWriter, request *http.Request, _ bool) (bool, error) {
	if user, ok := request.Context().Value(authboss.CTXKeyUser).(*User); ok {
		authboss.PutSession(w, passwordHashKey, user.PasswordHash)
	}
	return false, nil
}

// mailVerification builds the verification mail and hands it to the configured mailer, which in
// production sends in the background.
func mailVerification(ctx context.Context, ab *authboss.Config, recipient, token string) error {
	link, err := verificationLink(ab.Paths.RootURL, token)
	if err != nil {
		return err
	}
	email, err := verificationEmail(ab.Mail.From, recipient, link)
	if err != nil {
		return err
	}
	return ab.Core.Mailer.Send(ctx, email)
}

// verificationLink puts the route in the path and the token in the fragment of the API root, so a
// root with a query still links to the route. The fragment never reaches a server or a log.
func verificationLink(root, token string) (string, error) {
	link, err := url.Parse(root)
	if err != nil {
		return "", err
	}
	// Append to the escaped root path without cleaning it, so %2F and empty segments stay as configured.
	escaped := strings.TrimSuffix(link.EscapedPath(), "/") + "/auth/verify-email"
	if link.Path, err = url.PathUnescape(escaped); err != nil {
		return "", err
	}
	link.RawPath = escaped
	link.RawQuery, link.ForceQuery = "", false
	link.Fragment, link.RawFragment = "token="+token, ""
	return link.String(), nil
}

// verifyEmail turns a verification token into proof of the account's address. Every token problem
// answers the same 422, so a caller cannot tell an unknown token from a used or expired one. It
// reads and writes no session.
func (accounts *Auth) verifyEmail(ctx *gin.Context) {
	problem := func(status int, code, message string) {
		ctx.JSON(status, api.Problem{Code: code, Message: message})
	}
	invalid := func() {
		problem(http.StatusUnprocessableEntity, "invalid_token", "The verification link does not work.")
	}
	failed := func(step string, err error) {
		// No token, digest, password or email: only the step and the error type.
		slog.Error("verifying the email failed", "step", step, "error_type", errorType(err))
		problem(http.StatusInternalServerError, "internal", "internal error")
	}
	queries := accounts.storer.store.Queries()

	allowed, retryAfter, err := hit(ctx, queries, rateLimitBucketVerify, ipKey(sourceIP(ctx.Request, accounts.trustedProxyHops)), verifyLimit, rateLimitWindow)
	if err != nil {
		failed("source limit", err)
		return
	}
	if !allowed {
		ctx.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		problem(http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.")
		return
	}

	var input api.VerificationInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		problem(http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	// Strict refuses a spelling with nonzero padding bits, so each token has one form.
	raw, err := base64.URLEncoding.Strict().DecodeString(input.Token)
	generator := authboss.NewSha512TokenGenerator()
	if err != nil || len(raw) != generator.TokenSize() {
		problem(http.StatusBadRequest, "bad_request", "token is not valid")
		return
	}
	selector, verifier := generator.ParseToken(string(raw))

	row, err := queries.GetUserByEmailVerificationSelector(ctx, selector)
	if db.IsNoRows(err) {
		subtle.ConstantTimeCompare(verifier, dummyVerifier)
		invalid()
		return
	}
	if err != nil {
		failed("lookup", err)
		return
	}
	if len(verifier) != len(row.EmailVerificationVerifier) || subtle.ConstantTimeCompare(verifier, row.EmailVerificationVerifier) != 1 {
		invalid()
		return
	}

	// Ten tries per challenge across all sources, so the password cannot be guessed through this route.
	allowed, _, err = hit(ctx, queries, rateLimitBucketVerifyPassword, selector, verifyPasswordLimit, rateLimitWindow)
	if err != nil {
		failed("password limit", err)
		return
	}
	if !allowed {
		invalid()
		return
	}

	// A Google-only row has no password and ignores any password in the request. The attempt counter and
	// the lock stay as they are: anyone with the token could otherwise lock the account.
	if row.PasswordHash != nil && *row.PasswordHash != "" {
		if input.Password == nil ||
			accounts.authboss.Core.Hasher.CompareHashAndPassword(*row.PasswordHash, *input.Password) != nil {
			invalid()
			return
		}
	}

	err = accounts.storer.store.InTransaction(ctx, func(queries gen.Querier) error {
		// The expiry check must run after the row lock: a holder can release the row unchanged, and then
		// PostgreSQL does not re-check the update's conditions.
		if _, err := queries.LockUserForCurrency(ctx, row.ID); db.IsNoRows(err) {
			return errInvalidToken
		} else if err != nil {
			return err
		}
		_, err := queries.ConsumeEmailVerification(ctx, gen.ConsumeEmailVerificationParams{
			ID: row.ID, Selector: selector, Verifier: verifier, PasswordHash: row.PasswordHash, Email: row.Email,
		})
		if db.IsNoRows(err) {
			return errInvalidToken
		}
		if err != nil {
			return err
		}
		return queries.ClaimUnownedData(ctx, row.ID)
	})
	if errors.Is(err, errInvalidToken) {
		invalid()
		return
	}
	if err != nil {
		failed("consume", err)
		return
	}
	ctx.JSON(http.StatusOK, api.StatusMessage{Message: "Your email address is verified. You can now sign in."})
}

// resendVerification mails a new verification link to an unverified, unlocked account. Every answer
// that does not fail the request is the same 202, so the caller cannot tell which accounts exist. The
// mailer does not wait for delivery, and a send error only reaches the log.
func (accounts *Auth) resendVerification(ctx *gin.Context) {
	problem := func(status int, code, message string) {
		ctx.JSON(status, api.Problem{Code: code, Message: message})
	}
	queries := accounts.storer.store.Queries()

	allowed, retryAfter, err := hit(ctx, queries, rateLimitBucketResend, ipKey(sourceIP(ctx.Request, accounts.trustedProxyHops)), resendLimit, rateLimitWindow)
	if err != nil {
		slog.Error("resending the verification email failed", "step", "source limit", "error_type", errorType(err))
		problem(http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if !allowed {
		ctx.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		problem(http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.")
		return
	}

	var input api.VerificationResendInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		problem(http.StatusBadRequest, "bad_request", "email is required")
		return
	}
	address := strings.TrimSpace(input.Email)
	// ParseAddress accepts a display name; the address must be the whole text.
	if parsed, err := mail.ParseAddress(address); err != nil || parsed.Address != address || utf8.RuneCountInString(address) > maxEmailLength {
		problem(http.StatusBadRequest, "bad_request", "email is not valid")
		return
	}

	accepted := func() {
		ctx.JSON(http.StatusAccepted, api.StatusMessage{Message: "If the address belongs to an account that still needs verification, a new link is on its way."})
	}
	// Same condition as Storer.Create: any other mode, the zero value included, issues nothing.
	if mode := accounts.storer.mode; mode != EmailVerificationSend && mode != EmailVerificationEnforce {
		accepted()
		return
	}
	email := normalizeEmail(address)
	token, err := issueVerification(ctx, queries, email)
	if db.IsNoRows(err) { // unknown, verified, locked, cooldown or window limit: same answer, no mail
		accepted()
		return
	}
	if err != nil {
		slog.Error("resending the verification email failed", "step", "issue", "error_type", errorType(err))
		problem(http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := mailVerification(ctx.Request.Context(), &accounts.authboss.Config, email, token); err != nil {
		// No recipient, token or link: the error text can echo them.
		slog.Error("sending the verification email failed")
	}
	accepted()
}

// errorType names the kind of an error without its text, which can echo a value.
func errorType(err error) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
}
