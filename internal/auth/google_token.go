package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/api/idtoken"

	"meta-frames-server/internal/api"
)

// googleTokenRoute signs in a mobile app. The app gets an ID token from the device's Google
// account picker, so there is no browser to walk through the /oauth2 redirects.
const googleTokenRoute = "/google/token"

// googleConfigRoute tells a mobile app which web client to name as serverClientId, so each server
// supplies its own and the app's build holds none.
const googleConfigRoute = "/google/config"

// GoogleIDClaims are the parts of a Google ID token that sign-in reads.
type GoogleIDClaims struct {
	Audience      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// GoogleIDTokenVerifier checks a token's signature, expiry and issuer and returns its claims. It
// does not check the audience: Auth compares that with GoogleConfig.audiences.
type GoogleIDTokenVerifier func(ctx context.Context, idToken string) (GoogleIDClaims, error)

// audiences are the OAuth clients whose ID tokens this server accepts: the mobile clients, and the
// web client, which Android's Credential Manager names as the audience.
func (google GoogleConfig) audiences() []string {
	audiences := slices.Clone(google.MobileClientIDs)
	if google.ClientID != "" {
		audiences = append(audiences, google.ClientID)
	}
	return audiences
}

func (google GoogleConfig) idTokenVerifier() GoogleIDTokenVerifier {
	if google.VerifyIDToken != nil {
		return google.VerifyIDToken
	}
	return verifyGoogleIDToken
}

// verifyGoogleIDToken checks the token against Google's published keys. An empty audience tells
// idtoken.Validate to skip that check.
func verifyGoogleIDToken(ctx context.Context, idToken string) (GoogleIDClaims, error) {
	payload, err := idtoken.Validate(ctx, idToken, "")
	if err != nil {
		return GoogleIDClaims{}, err
	}
	claim := func(name string) string {
		value, _ := payload.Claims[name].(string)
		return value
	}
	return GoogleIDClaims{
		Audience:      payload.Audience,
		Subject:       payload.Subject,
		Email:         claim("email"),
		EmailVerified: fmt.Sprint(payload.Claims["email_verified"]) == "true",
		Name:          claim("name"),
	}, nil
}

// googleConfig answers with the web client id. A client id is public, unlike the client secret.
func (accounts *Auth) googleConfig(ctx *gin.Context) {
	if accounts.googleWebClientID == "" {
		ctx.JSON(http.StatusNotFound, api.Problem{Code: "not_configured", Message: "sign-in with Google is not configured"})
		return
	}
	ctx.JSON(http.StatusOK, api.GoogleConfig{ServerClientId: accounts.googleWebClientID})
}

// signInWithGoogleToken finds or creates the account the same way the browser flow does, then
// starts a session. It answers with the session cookie and JSON, like /auth/login.
func (accounts *Auth) signInWithGoogleToken(ctx *gin.Context) {
	problem := func(status int, code, message string) {
		ctx.JSON(status, api.Problem{Code: code, Message: message})
	}
	if len(accounts.googleAudiences) == 0 {
		problem(http.StatusNotFound, "not_configured", "sign-in with Google is not configured")
		return
	}
	var input api.GoogleTokenInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		problem(http.StatusBadRequest, "bad_request", "idToken is required")
		return
	}
	const invalidToken = "The Google sign-in could not be verified."
	claims, err := accounts.verifyGoogleToken(ctx, input.IdToken)
	if err != nil {
		slog.Warn("google id token refused", "err", err)
		problem(http.StatusUnauthorized, "invalid_google_token", invalidToken)
		return
	}
	if !slices.Contains(accounts.googleAudiences, claims.Audience) {
		slog.Warn("google id token for another client", "audience", claims.Audience)
		problem(http.StatusUnauthorized, "invalid_google_token", invalidToken)
		return
	}

	found, err := accounts.storer.NewFromOAuth2(ctx, googleProvider, map[string]string{
		detailUID:           claims.Subject,
		detailEmail:         claims.Email,
		detailEmailVerified: strconv.FormatBool(claims.EmailVerified),
		detailName:          claims.Name,
	})
	switch {
	case errors.Is(err, ErrEmailNotVerified):
		problem(http.StatusForbidden, "email_not_verified", "Your Google email address is not verified.")
		return
	case errors.Is(err, ErrIdentityConflict):
		problem(http.StatusConflict, "account_conflict", "This email's account is linked to another Google account.")
		return
	case err != nil:
		accounts.tokenSignInFailed(ctx, err)
		return
	}
	user, err := asUser(found)
	if err != nil {
		accounts.tokenSignInFailed(ctx, err)
		return
	}
	if user.LockedUntil.After(time.Now()) {
		problem(http.StatusTooManyRequests, "account_locked", "Your account is locked. Try again in 15 minutes.")
		return
	}
	if err := accounts.storer.SaveOAuth2(ctx, user); err != nil {
		accounts.tokenSignInFailed(ctx, err)
		return
	}
	if err := accounts.replaceSession(ctx, user); err != nil {
		accounts.tokenSignInFailed(ctx, err)
		return
	}
	view := user.API()
	ctx.JSON(http.StatusOK, api.AuthResult{Message: "Logged in", User: &view})
}

// replaceSession ends the request's session, if any, and starts a new one with a fresh token, as
// every other sign-in does.
func (accounts *Auth) replaceSession(ctx *gin.Context, user *User) error {
	_, tokenHash, err := accounts.sessions.lookup(ctx.Request)
	if err != nil {
		return err
	}
	if tokenHash != nil {
		if err := accounts.sessions.store.Queries().DeleteSession(ctx, tokenHash); err != nil {
			return err
		}
	}
	return accounts.sessions.start(ctx, ctx.Writer, user.Email)
}

func (accounts *Auth) tokenSignInFailed(ctx *gin.Context, err error) {
	slog.Error("google token sign-in failed", "err", err)
	ctx.JSON(http.StatusInternalServerError, api.Problem{Code: "internal", Message: "internal error"})
}
