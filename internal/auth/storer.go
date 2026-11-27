package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
)

// Storer keeps authboss accounts in the app_user table.
type Storer struct {
	store db.Store
	mode  EmailVerificationMode // set by New; send and enforce issue a challenge on registration
}

var (
	_ authboss.CreatingServerStorer   = (*Storer)(nil)
	_ authboss.RecoveringServerStorer = (*Storer)(nil)
	_ authboss.OAuth2ServerStorer     = (*Storer)(nil)
)

var (
	// ErrEmailNotVerified refuses a Google sign-in whose email Google has not verified: it could
	// claim someone else's account or address.
	ErrEmailNotVerified = errors.New("auth: the provider has not verified this email")
	// ErrIdentityConflict refuses a Google sign-in whose email belongs to another account: a
	// password account or an account linked to a different Google identity.
	ErrIdentityConflict = errors.New("auth: the email already belongs to another account")
	// ErrAccountLocked refuses a sign-in to a locked linked account before any write.
	ErrAccountLocked = errors.New("auth: the account is locked")
)

func NewStorer(store db.Store) *Storer { return &Storer{store: store} }

// Load finds a user by email (the authboss primary id) or by an oauth2 pid
// ("oauth2;;google;;<uid>").
func (storer *Storer) Load(ctx context.Context, pid string) (authboss.User, error) {
	var row gen.AppUser
	var err error
	if strings.HasPrefix(pid, oauth2PIDPrefix) {
		provider, uid, parseErr := authboss.ParseOAuth2PID(pid)
		if parseErr != nil {
			return nil, authboss.ErrUserNotFound
		}
		row, err = storer.store.Queries().GetUserByOAuth2(ctx, gen.GetUserByOAuth2Params{Oauth2Provider: &provider, Oauth2Uid: &uid})
	} else {
		row, err = storer.store.Queries().GetUserByEmail(ctx, normalizeEmail(pid))
	}
	if db.IsNoRows(err) {
		return nil, authboss.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return fromRow(row), nil
}

// Save writes back the fields authboss changes. Credentials (password and recovery fields) go out
// only after a credential setter ran; login bookkeeping writes name, lockout state and avatar.
func (storer *Storer) Save(ctx context.Context, user authboss.User) error {
	account, err := asUser(user)
	if err != nil {
		return err
	}
	if account.passwordChanged || account.recoveryChanged {
		_, err = storer.store.Queries().UpdateUser(ctx, gen.UpdateUserParams{
			ID:                 account.ID,
			SetPassword:        account.passwordChanged,
			SetRecovery:        account.recoveryChanged,
			Name:               emptyToNil(account.Name),
			PasswordHash:       emptyToNil(account.PasswordHash),
			RecoverSelector:    emptyToNil(account.RecoverSelector),
			RecoverVerifier:    emptyToNil(account.RecoverVerifier),
			RecoverTokenExpiry: timestamp(account.RecoverExpiry),
			AttemptCount:       int32(min(account.AttemptCount, math.MaxInt32)),
			LastAttempt:        timestamp(account.LastAttempt),
			LockedUntil:        timestamp(account.LockedUntil),
			Oauth2Provider:     emptyToNil(account.OAuth2Provider),
			Oauth2Uid:          emptyToNil(account.OAuth2UID),
			AvatarUrl:          emptyToNil(account.AvatarURL),
		})
	} else {
		_, err = storer.store.Queries().UpdateUserState(ctx, gen.UpdateUserStateParams{
			ID:             account.ID,
			Name:           emptyToNil(account.Name),
			AttemptCount:   int32(min(account.AttemptCount, math.MaxInt32)),
			LastAttempt:    timestamp(account.LastAttempt),
			LockedUntil:    timestamp(account.LockedUntil),
			AvatarUrl:      emptyToNil(account.AvatarURL),
			Oauth2Provider: emptyToNil(account.OAuth2Provider),
			Oauth2Uid:      emptyToNil(account.OAuth2UID),
		})
	}
	if db.IsNoRows(err) {
		return authboss.ErrUserNotFound
	}
	if err == nil {
		account.passwordChanged, account.recoveryChanged = false, false
	}
	return err
}

// New returns a blank user for registration to fill in.
func (*Storer) New(context.Context) authboss.User { return &User{} }

// Create inserts a registered user and fills in its id and creation time.
func (storer *Storer) Create(ctx context.Context, user authboss.User) error {
	account, err := asUser(user)
	if err != nil {
		return err
	}
	var row gen.AppUser
	var token string
	err = storer.store.InTransaction(ctx, func(queries gen.Querier) error {
		row, err = queries.InsertUser(ctx, gen.InsertUserParams{
			ID:             uuid.New(),
			Email:          normalizeEmail(account.Email),
			Name:           emptyToNil(account.Name),
			PasswordHash:   emptyToNil(account.PasswordHash),
			Oauth2Provider: emptyToNil(account.OAuth2Provider),
			Oauth2Uid:      emptyToNil(account.OAuth2UID),
			AvatarUrl:      emptyToNil(account.AvatarURL),
		})
		if db.IsUniqueViolation(err) { // only the insert means the email is taken
			return authboss.ErrUserFound
		}
		if err != nil {
			return err
		}
		issue := account.OAuth2UID == "" && (storer.mode == EmailVerificationSend || storer.mode == EmailVerificationEnforce)
		if account.OAuth2UID != "" {
			if err = queries.MarkEmailVerifiedByGoogle(ctx, row.ID); err != nil {
				return err
			}
		}
		if issue {
			// A password account claims old data only after it proves the address.
			if token, err = issueVerification(ctx, queries, row.Email); err != nil {
				return err
			}
		} else if err = queries.ClaimUnownedData(ctx, row.ID); err != nil {
			// Records from before accounts existed go to the first account (later ones find none).
			return err
		}
		// The claim can lock the currency, so return the row as it is after the claim.
		row, err = queries.GetUserByID(ctx, row.ID)
		return err
	})
	if err != nil {
		return err
	}
	*account = *fromRow(row)
	account.verificationToken = token
	return nil
}

// issueVerification stores the digests of a new token for the email and returns the raw token. No
// row from the query (cooldown, window, lock) is an error: a new account must get its challenge.
func issueVerification(ctx context.Context, queries gen.Querier, email string) (string, error) {
	selector, verifier, token, err := authboss.NewSha512TokenGenerator().GenerateToken()
	if err != nil {
		return "", err
	}
	selectorBytes, err := base64.StdEncoding.DecodeString(selector)
	if err != nil {
		return "", err
	}
	verifierBytes, err := base64.StdEncoding.DecodeString(verifier)
	if err != nil {
		return "", err
	}
	if _, err = queries.IssueEmailVerification(ctx, gen.IssueEmailVerificationParams{Email: email, Selector: selectorBytes, Verifier: verifierBytes}); err != nil {
		return "", fmt.Errorf("auth: issuing the email verification: %w", err)
	}
	return token, nil
}

// LoadByRecoverSelector finds the user a password-reset token was issued to.
func (storer *Storer) LoadByRecoverSelector(ctx context.Context, selector string) (authboss.RecoverableUser, error) {
	row, err := storer.store.Queries().GetUserByRecoverSelector(ctx, &selector)
	if db.IsNoRows(err) {
		return nil, authboss.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return fromRow(row), nil
}

// NewFromOAuth2 finds or prepares the account for a Google sign-in: the account already linked to
// the identity, else a new account. An email that already belongs to another account is refused
// with ErrIdentityConflict until email verification exists (AUTH-15). SaveOAuth2 writes it.
func (storer *Storer) NewFromOAuth2(ctx context.Context, provider string, details map[string]string) (authboss.OAuth2User, error) {
	uid, email := details[detailUID], normalizeEmail(details[detailEmail])
	if uid == "" || email == "" {
		return nil, fmt.Errorf("auth: %s returned no id or email", provider)
	}
	queries := storer.store.Queries()
	row, err := queries.GetUserByOAuth2(ctx, gen.GetUserByOAuth2Params{Oauth2Provider: &provider, Oauth2Uid: &uid})
	if err == nil {
		linked := fromRow(row)
		if linked.LockedUntil.After(time.Now()) {
			return nil, ErrAccountLocked
		}
		return withProfile(linked, details), nil
	}
	if !db.IsNoRows(err) {
		return nil, err
	}
	if details[detailEmailVerified] != "true" {
		return nil, ErrEmailNotVerified
	}

	_, err = queries.GetUserByEmail(ctx, email)
	if db.IsNoRows(err) {
		return withProfile(&User{Email: email, OAuth2Provider: provider, OAuth2UID: uid}, details), nil
	}
	if err != nil {
		return nil, err
	}
	return nil, ErrIdentityConflict
}

// withProfile fills in the provider's display name when the account has none, and takes the
// provider's current picture: the account cannot set its own, and Google changes the URL.
func withProfile(user *User, details map[string]string) *User {
	if user.Name == "" {
		user.Name = details[detailName]
	}
	if avatar := httpsURL(details[detailAvatar]); avatar != "" {
		user.AvatarURL = avatar
	}
	return user
}

// httpsURL keeps only an absolute https URL, so a client can put it in an <img> as it is.
func httpsURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return raw
}

// SaveOAuth2 inserts a new account or updates a linked one. A stale result is ErrIdentityConflict.
func (storer *Storer) SaveOAuth2(ctx context.Context, user authboss.OAuth2User) error {
	account, err := asUser(user)
	if err != nil {
		return err
	}
	// A stale result means another request changed or created the account after NewFromOAuth2 read it.
	if account.ID == uuid.Nil {
		err = storer.Create(ctx, account)
		if errors.Is(err, authboss.ErrUserFound) {
			return ErrIdentityConflict
		}
		return err
	}
	err = storer.Save(ctx, account)
	if errors.Is(err, authboss.ErrUserNotFound) {
		return ErrIdentityConflict
	}
	return err
}

// timestamp stores the zero time as NULL.
func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func asUser(user authboss.User) (*User, error) {
	account, ok := user.(*User)
	if !ok {
		return nil, fmt.Errorf("auth: unexpected user type %T", user)
	}
	return account, nil
}
