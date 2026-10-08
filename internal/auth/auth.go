// Package auth adds accounts with authboss: registration, password login (with lockout after
// repeated wrong passwords), sign-in with Google, logout and password recovery under /auth, plus the signed-in user
// for every other handler (GET /auth/me, and the session check in internal/server).
//
// Sessions live in the user_session table behind an HTTP-only cookie, so they can be ended
// server-side.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aarondl/authboss/v3"
	_ "github.com/aarondl/authboss/v3/auth" // modules register themselves with authboss
	"github.com/aarondl/authboss/v3/defaults"
	_ "github.com/aarondl/authboss/v3/lock"
	_ "github.com/aarondl/authboss/v3/logout"
	_ "github.com/aarondl/authboss/v3/oauth2"
	_ "github.com/aarondl/authboss/v3/recover"
	_ "github.com/aarondl/authboss/v3/register"
	"github.com/gin-gonic/gin"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db"
)

const (
	// SessionCookie is the cookie that carries the session (see securitySchemes in the spec).
	SessionCookie = "metaframes_session"
	// OAuth2Cookie holds the Google sign-in state between the start route and the callback.
	OAuth2Cookie = "metaframes_oauth2"

	mountPath         = "/auth"
	sessionMaxAge     = 7 * 24 * time.Hour
	oauth2StateMaxAge = 10 * time.Minute
	fieldName         = "name"

	// Five wrong passwords within 15 minutes lock the account for 15 minutes. The lock is short
	// because anyone who knows an email can trigger it.
	lockAfter    = 5
	lockWindow   = 15 * time.Minute
	lockDuration = 15 * time.Minute
)

// routes are the authboss endpoints the API exposes, relative to mountPath. The modules also
// register GET pages for server-rendered forms; those stay unrouted.
var routes = []string{"/register", "/login", "/logout", "/recover", "/recover/end"}

// googleRoutes start Google sign-in and receive Google's redirect back. They are GETs because the
// browser navigates to them.
var googleRoutes = []string{"/oauth2/" + googleProvider, "/oauth2/callback/" + googleProvider}

type Config struct {
	// SecureCookie sends the session cookie over HTTPS only.
	SecureCookie bool
	// SameSite is the session cookie's SameSite mode; zero means Lax.
	SameSite http.SameSite
	// AppURL is the front-end root. Recovery emails link to {AppURL}/recover/end?token=...; Google
	// sign-in returns to {AppURL}{redir} or {AppURL}/login?error=<code>.
	AppURL string
	// APIURL is this server's public root; Google redirects back to {APIURL}/auth/oauth2/callback/google.
	APIURL   string
	MailFrom string
	// Mailer sends the recovery email. Nil prints emails to stdout instead.
	Mailer authboss.Mailer
	// Google enables "Sign in with Google" when its client id and secret are set.
	Google GoogleConfig
}

// Auth is the configured authboss instance.
type Auth struct {
	authboss *authboss.Authboss
	storer   *Storer
	sessions *sessionStore
	google   bool
}

func New(store db.Store, config Config) (*Auth, error) {
	mailer := config.Mailer
	if mailer == nil {
		mailer = defaults.NewLogMailer(os.Stdout)
	}
	sameSite := config.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}

	storer := NewStorer(store)
	accounts := &Auth{
		authboss: authboss.New(),
		storer:   storer,
		sessions: &sessionStore{store: store, storer: storer, secure: config.SecureCookie, sameSite: sameSite},
		google:   config.Google.enabled(),
	}
	appURL := strings.TrimRight(config.AppURL, "/")
	browser := browserFlow{appURL: appURL}
	ab := &accounts.authboss.Config
	ab.Paths.Mount = mountPath
	ab.Paths.RootURL = strings.TrimRight(config.APIURL, "/")
	ab.Paths.OAuth2LoginOK = "/"
	ab.Mail.RootURL = appURL
	ab.Mail.From = config.MailFrom
	ab.Mail.FromName = "Meta-Frame"
	ab.Mail.SubjectPrefix = "[Meta-Frame] "
	ab.Storage.Server = accounts.storer
	ab.Storage.SessionState = accounts.sessions
	ab.Modules.LogoutMethod = http.MethodPost
	ab.Modules.MailNoGoroutine = true // the request context is cancelled once the response is sent
	ab.Modules.LockAfter = lockAfter
	ab.Modules.LockWindow = lockWindow
	ab.Modules.LockDuration = lockDuration
	ab.Core.Router = defaults.NewRouter()
	ab.Core.ErrorHandler = errorHandler{browser: browser}
	ab.Core.Responder = responder{}
	ab.Core.Redirector = redirector{browser: browser}
	ab.Core.BodyReader = newBodyReader()
	ab.Core.ViewRenderer = defaults.JSONRenderer{} // modules only Load their pages; responder writes the JSON
	ab.Core.MailRenderer = mailRenderer{}
	ab.Core.Mailer = mailer
	ab.Core.Logger = logger{}

	modules := []string{"auth", "register", "recover", "logout", "lock"}
	if accounts.google {
		ab.Modules.OAuth2Providers = map[string]authboss.OAuth2Provider{googleProvider: config.Google.provider()}
		modules = append(modules, "oauth2")
	}
	if err := accounts.authboss.Init(modules...); err != nil {
		return nil, err
	}
	accounts.authboss.Events.After(authboss.EventRecoverEnd, accounts.afterPasswordReset)
	return accounts, nil
}

// newBodyReader reads JSON bodies with authboss's default rules: a valid email, and a password of
// at least 8 characters with an upper-case letter, a lower-case letter, a digit and a symbol.
func newBodyReader() *defaults.HTTPBodyReader {
	reader := defaults.NewHTTPBodyReader(true, false)
	reader.Confirms = nil // clients ask for the password twice themselves; the API takes it once
	reader.Whitelist["register"] = append(reader.Whitelist["register"], fieldName)
	return reader
}

// afterPasswordReset ends every session of the account, since one of them may belong to whoever
// made the reset necessary, and lifts a lockout: the reset proved the owner holds the mailbox.
func (accounts *Auth) afterPasswordReset(_ http.ResponseWriter, request *http.Request, _ bool) (bool, error) {
	user, ok := request.Context().Value(authboss.CTXKeyUser).(*User)
	if !ok {
		return false, nil
	}
	user.AttemptCount, user.LockedUntil = 0, time.Time{}
	if err := accounts.storer.Save(request.Context(), user); err != nil {
		return false, err
	}
	return false, accounts.sessions.RevokeAll(request.Context(), user)
}

// Mount routes the authboss endpoints under /auth.
func (accounts *Auth) Mount(router gin.IRoutes) {
	ab := accounts.authboss
	handler := gin.WrapH(http.StripPrefix(mountPath, ab.LoadClientStateMiddleware(ab.Config.Core.Router)))
	for _, route := range routes {
		router.POST(mountPath+route, handler)
	}
	for _, route := range googleRoutes {
		if accounts.google {
			router.GET(mountPath+route, handler)
		} else {
			router.GET(mountPath+route, func(ctx *gin.Context) {
				ctx.JSON(http.StatusNotFound, api.Problem{Code: "not_configured", Message: "sign-in with Google is not configured"})
			})
		}
	}
}

// LoadUser puts the signed-in user, if any, into the request context for CurrentUser.
// A missing, unknown or expired session leaves the request anonymous.
func (accounts *Auth) LoadUser() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, _, err := accounts.sessions.lookup(ctx.Request)
		if err != nil {
			slog.Error("loading the session user failed", "path", ctx.Request.URL.Path, "err", err)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, api.Problem{Code: "internal", Message: "internal error"})
			return
		}
		if user != nil {
			ctx.Request = ctx.Request.WithContext(WithUser(ctx.Request.Context(), user))
		}
		ctx.Next()
	}
}

type userKey struct{}

// WithUser returns a context carrying the signed-in user.
func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// CurrentUser returns the user stored by LoadUser, if the request has one.
func CurrentUser(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userKey{}).(*User)
	return user, ok
}
