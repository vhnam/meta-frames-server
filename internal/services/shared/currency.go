package shared

import (
	"context"
	"slices"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
)

// Currency is the currency state of the signed-in account as read by LockAccountCurrency.
type Currency struct {
	Code   string
	Locked bool
}

var currencyCodes = []string{"VND", "USD", "EUR"}

// LockAccountCurrency is step A of the money guard. It reads the session account; an unlocked
// account is read again with FOR NO KEY UPDATE, so the setting save and every first money write
// take turns, and the state returned is the one read under that lock. A locked account never
// changes code, so it takes no row lock. Take this lock before any child row lock. It writes nothing.
func LockAccountCurrency(ctx context.Context, queries gen.Querier) (Currency, error) {
	owner := requestctx.Owner(ctx)
	account, err := queries.GetUserByID(ctx, owner)
	if err != nil {
		return Currency{}, NotFoundOr(err, "account")
	}
	if !account.CurrencyLocked {
		if account, err = queries.LockUserForCurrency(ctx, owner); err != nil {
			return Currency{}, NotFoundOr(err, "account")
		}
	}
	return Currency{Code: account.CurrencyCode, Locked: account.CurrencyLocked}, nil
}

// ConfirmCurrency is step B of the money guard. The Expected-Currency header must name the account
// currency; no header means VND. A match on an unlocked account sets the lock, in the caller's
// transaction, so a failed write rolls the lock back with it.
func ConfirmCurrency(ctx context.Context, queries gen.Querier, state Currency) error {
	meta, _ := requestctx.From(ctx)
	code := "VND"
	switch values := meta.ExpectedCurrencyValues(); {
	case len(values) == 0:
	case len(values) == 1 && slices.Contains(currencyCodes, values[0]):
		code = values[0]
	default:
		return apperror.BadRequest("bad_request", "Expected-Currency must be one of VND, USD or EUR")
	}
	if code != state.Code {
		return apperror.Conflict("currency_mismatch", "the account currency is "+state.Code)
	}
	if state.Locked {
		return nil
	}
	return queries.SetUserCurrencyLocked(ctx, requestctx.Owner(ctx))
}

// GuardMoneyWrite runs both steps for a write that records a price.
func GuardMoneyWrite(ctx context.Context, queries gen.Querier) error {
	state, err := LockAccountCurrency(ctx, queries)
	if err != nil {
		return err
	}
	return ConfirmCurrency(ctx, queries, state)
}
