package auth

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	// ErrIdentityConflict refuses a Google sign-in whose email belongs to an account already
	// linked to a different Google identity.
	ErrIdentityConflict = errors.New("auth: the email's account is linked to another identity")
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

// Save writes back the fields authboss changes: password, name, recovery and lockout state.
func (storer *Storer) Save(ctx context.Context, user authboss.User) error {
	account, err := asUser(user)
	if err != nil {
		return err
	}
	_, err = storer.store.Queries().UpdateUser(ctx, gen.UpdateUserParams{
		ID:                 account.ID,
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
	})
	if db.IsNoRows(err) {
		return authboss.ErrUserNotFound
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
	err = storer.store.InTransaction(ctx, func(queries gen.Querier) error {
		row, err = queries.InsertUser(ctx, gen.InsertUserParams{
			ID:             uuid.New(),
			Email:          normalizeEmail(account.Email),
			Name:           emptyToNil(account.Name),
			PasswordHash:   emptyToNil(account.PasswordHash),
			Oauth2Provider: emptyToNil(account.OAuth2Provider),
			Oauth2Uid:      emptyToNil(account.OAuth2UID),
		})
		if err != nil {
			return err
		}
		// Records from before accounts existed go to the first account (later ones find none).
		return queries.ClaimUnownedData(ctx, row.ID)
	})
	if db.IsUniqueViolation(err) {
		return authboss.ErrUserFound
	}
	if err != nil {
		return err
	}
	*account = *fromRow(row)
	return nil
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
// the identity, else the account with the same (verified) email, which gets linked, else a new
// account. SaveOAuth2 writes it.
func (storer *Storer) NewFromOAuth2(ctx context.Context, provider string, details map[string]string) (authboss.OAuth2User, error) {
	uid, email := details[detailUID], normalizeEmail(details[detailEmail])
	if uid == "" || email == "" {
		return nil, fmt.Errorf("auth: %s returned no id or email", provider)
	}
	queries := storer.store.Queries()
	row, err := queries.GetUserByOAuth2(ctx, gen.GetUserByOAuth2Params{Oauth2Provider: &provider, Oauth2Uid: &uid})
	if err == nil {
		return withName(fromRow(row), details[detailName]), nil
	}
	if !db.IsNoRows(err) {
		return nil, err
	}
	if details[detailEmailVerified] != "true" {
		return nil, ErrEmailNotVerified
	}

	row, err = queries.GetUserByEmail(ctx, email)
	if db.IsNoRows(err) {
		return &User{Email: email, Name: details[detailName], OAuth2Provider: provider, OAuth2UID: uid}, nil
	}
	if err != nil {
		return nil, err
	}
	existing := fromRow(row)
	if existing.OAuth2UID != "" {
		return nil, ErrIdentityConflict
	}
	existing.OAuth2Provider, existing.OAuth2UID = provider, uid
	return withName(existing, details[detailName]), nil
}

// withName fills in the provider's display name when the account has none.
func withName(user *User, name string) *User {
	if user.Name == "" {
		user.Name = name
	}
	return user
}

// SaveOAuth2 inserts a new account or updates a linked one.
func (storer *Storer) SaveOAuth2(ctx context.Context, user authboss.OAuth2User) error {
	account, err := asUser(user)
	if err != nil {
		return err
	}
	if account.ID == uuid.Nil {
		return storer.Create(ctx, account)
	}
	return storer.Save(ctx, account)
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
