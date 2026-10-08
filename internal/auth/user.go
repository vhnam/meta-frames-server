package auth

import (
	"strings"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/db/gen"
)

// User is an account as authboss sees it. The email is the primary id (authboss's "PID").
type User struct {
	ID              uuid.UUID
	Email           string
	Name            string
	PasswordHash    string
	RecoverSelector string
	RecoverVerifier string
	RecoverExpiry   time.Time
	AttemptCount    int
	LastAttempt     time.Time
	LockedUntil     time.Time
	OAuth2Provider  string // "google" for an account linked to Google
	OAuth2UID       string // the provider's id for the account ("sub")
	CreatedAt       time.Time
}

var (
	_ authboss.AuthableUser    = (*User)(nil)
	_ authboss.RecoverableUser = (*User)(nil)
	_ authboss.ArbitraryUser   = (*User)(nil)
	_ authboss.LockableUser    = (*User)(nil)
	_ authboss.OAuth2User      = (*User)(nil)
)

// normalizeEmail makes "Ansel@Example.com " and "ansel@example.com" the same account.
func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func fromRow(row gen.AppUser) *User {
	return &User{
		ID:              row.ID,
		Email:           row.Email,
		Name:            valueOrEmpty(row.Name),
		PasswordHash:    valueOrEmpty(row.PasswordHash),
		RecoverSelector: valueOrEmpty(row.RecoverSelector),
		RecoverVerifier: valueOrEmpty(row.RecoverVerifier),
		RecoverExpiry:   convert.Timestamp(row.RecoverTokenExpiry),
		AttemptCount:    int(row.AttemptCount),
		LastAttempt:     convert.Timestamp(row.LastAttempt),
		LockedUntil:     convert.Timestamp(row.LockedUntil),
		OAuth2Provider:  valueOrEmpty(row.Oauth2Provider),
		OAuth2UID:       valueOrEmpty(row.Oauth2Uid),
		CreatedAt:       convert.Timestamp(row.CreatedAt),
	}
}

// API is the public view of the user; it never includes the password hash or recovery state.
func (user *User) API() api.User {
	view := api.User{Id: user.ID, Email: openapi_types.Email(user.Email), CreatedAt: user.CreatedAt}
	if user.Name != "" {
		name := user.Name
		view.Name = &name
	}
	return view
}

func (user *User) GetPID() string    { return user.Email }
func (user *User) PutPID(pid string) { user.Email = normalizeEmail(pid) }

func (user *User) GetPassword() string         { return user.PasswordHash }
func (user *User) PutPassword(password string) { user.PasswordHash = password }

func (user *User) GetEmail() string      { return user.Email }
func (user *User) PutEmail(email string) { user.Email = normalizeEmail(email) }

func (user *User) GetRecoverSelector() string         { return user.RecoverSelector }
func (user *User) PutRecoverSelector(selector string) { user.RecoverSelector = selector }
func (user *User) GetRecoverVerifier() string         { return user.RecoverVerifier }
func (user *User) PutRecoverVerifier(verifier string) { user.RecoverVerifier = verifier }
func (user *User) GetRecoverExpiry() time.Time        { return user.RecoverExpiry }
func (user *User) PutRecoverExpiry(expiry time.Time)  { user.RecoverExpiry = expiry }

// The lock module counts wrong passwords and locks the account for a while.
func (user *User) GetAttemptCount() int          { return user.AttemptCount }
func (user *User) PutAttemptCount(attempts int)  { user.AttemptCount = attempts }
func (user *User) GetLastAttempt() time.Time     { return user.LastAttempt }
func (user *User) PutLastAttempt(last time.Time) { user.LastAttempt = last }
func (user *User) GetLocked() time.Time          { return user.LockedUntil }
func (user *User) PutLocked(locked time.Time)    { user.LockedUntil = locked }

// The oauth2 module links the account to a Google identity. Its access and refresh tokens are
// dropped: the API never calls Google after sign-in, so keeping them would only be a liability.
func (user *User) IsOAuth2User() bool                { return user.OAuth2UID != "" }
func (user *User) GetOAuth2UID() string              { return user.OAuth2UID }
func (user *User) PutOAuth2UID(uid string)           { user.OAuth2UID = uid }
func (user *User) GetOAuth2Provider() string         { return user.OAuth2Provider }
func (user *User) PutOAuth2Provider(provider string) { user.OAuth2Provider = provider }
func (*User) GetOAuth2AccessToken() string           { return "" }
func (*User) PutOAuth2AccessToken(string)            {}
func (*User) GetOAuth2RefreshToken() string          { return "" }
func (*User) PutOAuth2RefreshToken(string)           {}
func (*User) GetOAuth2Expiry() time.Time             { return time.Time{} }
func (*User) PutOAuth2Expiry(time.Time)              {}

// GetArbitrary and PutArbitrary carry the extra registration fields (only "name").
func (user *User) GetArbitrary() map[string]string { return map[string]string{fieldName: user.Name} }
func (user *User) PutArbitrary(values map[string]string) {
	user.Name = strings.TrimSpace(values[fieldName])
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func emptyToNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
