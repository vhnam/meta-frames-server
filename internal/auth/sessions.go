package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
)

// sessionStore is authboss's session state, kept in the user_session table. The cookie carries
// a random token and the table keys on its SHA-256, so a copy of the table cannot be replayed as
// cookies. Deleting a row ends that session at once: on logout, and for every session of an
// account when its password is reset.
//
// Besides authboss.SessionKey (the signed-in user), the only values kept are the Google sign-in
// state and parameters, which live in a short-lived cookie of their own until the callback. Other
// keys, such as flash messages for server-rendered pages, are dropped.
type sessionStore struct {
	store    db.Store
	storer   *Storer
	secure   bool
	sameSite http.SameSite
}

// oauth2Keys are the session values the oauth2 module keeps between its start and callback.
var oauth2Keys = []string{authboss.SessionOAuth2State, authboss.SessionOAuth2Params}

var _ authboss.ClientStateReadWriter = (*sessionStore)(nil)

// sessionState is the session a request arrived with.
type sessionState struct {
	tokenHash []byte // nil when the request has no live session
	pid       string
	oauth2    map[string]string // from the OAuth2Cookie
}

func (state *sessionState) Get(key string) (string, bool) {
	if key == authboss.SessionKey && state.pid != "" {
		return state.pid, true
	}
	value, ok := state.oauth2[key]
	return value, ok
}

func (sessions *sessionStore) ReadState(request *http.Request) (authboss.ClientState, error) {
	user, tokenHash, err := sessions.lookup(request)
	if err != nil {
		return nil, err
	}
	state := &sessionState{tokenHash: tokenHash, oauth2: readOAuth2Cookie(request)}
	if user != nil {
		state.pid = user.Email
	}
	return state, nil
}

// readOAuth2Cookie decodes the sign-in state. It is not signed: it only has to match the state
// Google echoes back to the same browser, and a page on another site cannot set it.
func readOAuth2Cookie(request *http.Request) map[string]string {
	values := map[string]string{}
	cookie, err := request.Cookie(OAuth2Cookie)
	if err != nil {
		return values
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || json.Unmarshal(raw, &values) != nil {
		return map[string]string{}
	}
	return values
}

// lookup returns the user of the request's session cookie, or nil if the cookie is missing,
// unknown or expired.
func (sessions *sessionStore) lookup(request *http.Request) (*User, []byte, error) {
	cookie, err := request.Cookie(SessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, nil, nil
	}
	tokenHash := hashToken(cookie.Value)
	row, err := sessions.store.Queries().GetSessionUser(request.Context(), tokenHash)
	if db.IsNoRows(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return fromRow(row), tokenHash, nil
}

// WriteState applies authboss's changes. Every sign-in replaces the request's session with a
// fresh token (no session fixation); signing out deletes the row and clears the cookie.
func (sessions *sessionStore) WriteState(w http.ResponseWriter, state authboss.ClientState, events []authboss.ClientStateEvent) error {
	before := &sessionState{}
	if current, ok := state.(*sessionState); ok {
		before = current
	}
	pid, signedIn := before.pid, false
	oauth2 := maps.Clone(before.oauth2)
	if oauth2 == nil {
		oauth2 = map[string]string{}
	}
	for _, event := range events {
		switch event.Kind {
		case authboss.ClientStateEventPut:
			if event.Key == authboss.SessionKey {
				pid, signedIn = event.Value, true
			} else if slices.Contains(oauth2Keys, event.Key) {
				oauth2[event.Key] = event.Value
			}
		case authboss.ClientStateEventDel:
			if event.Key == authboss.SessionKey {
				pid = ""
			}
			delete(oauth2, event.Key)
		case authboss.ClientStateEventDelAll: // the key is a comma-separated list of keys to keep
			keep := strings.Split(event.Key, ",")
			if !slices.Contains(keep, authboss.SessionKey) {
				pid = ""
			}
			maps.DeleteFunc(oauth2, func(key, _ string) bool { return !slices.Contains(keep, key) })
		}
	}
	if !maps.Equal(oauth2, before.oauth2) {
		if err := sessions.writeOAuth2Cookie(w, oauth2); err != nil {
			return err
		}
	}
	if pid == before.pid && !signedIn {
		return nil
	}

	// authboss writes state while the response goes out, without the request context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if before.tokenHash != nil {
		if err := sessions.store.Queries().DeleteSession(ctx, before.tokenHash); err != nil {
			return err
		}
	}
	if pid == "" {
		http.SetCookie(w, sessions.cookie("", -1))
		return nil
	}
	return sessions.start(ctx, w, pid)
}

func (sessions *sessionStore) writeOAuth2Cookie(w http.ResponseWriter, values map[string]string) error {
	cookie := &http.Cookie{
		Name:     OAuth2Cookie,
		Path:     mountPath + "/oauth2",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   sessions.secure,
		SameSite: http.SameSiteLaxMode, // Google's redirect back is a top-level navigation from another site
	}
	if len(values) > 0 {
		raw, err := json.Marshal(values)
		if err != nil {
			return err
		}
		cookie.Value, cookie.MaxAge = base64.RawURLEncoding.EncodeToString(raw), int(oauth2StateMaxAge/time.Second)
	}
	http.SetCookie(w, cookie)
	return nil
}

func (sessions *sessionStore) start(ctx context.Context, w http.ResponseWriter, pid string) error {
	queries := sessions.store.Queries()
	user, err := sessions.storer.Load(ctx, pid) // an email, or an oauth2 pid after Google sign-in
	if err != nil {
		return err
	}
	if err := queries.DeleteExpiredSessions(ctx); err != nil {
		return err
	}
	token := rand.Text()
	err = queries.InsertSession(ctx, gen.InsertSessionParams{
		TokenHash: hashToken(token),
		UserID:    user.(*User).ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(sessionMaxAge), Valid: true},
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, sessions.cookie(token, int(sessionMaxAge/time.Second)))
	return nil
}

// RevokeAll ends every session of the user.
func (sessions *sessionStore) RevokeAll(ctx context.Context, user *User) error {
	return sessions.store.Queries().DeleteUserSessions(ctx, user.ID)
}

func (sessions *sessionStore) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   sessions.secure,
		SameSite: sessions.sameSite,
	}
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
