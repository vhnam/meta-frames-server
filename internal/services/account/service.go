// Package account holds the account settings that are not authboss's own.
package account

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
)

type Service struct{ store db.Store }

func New(store db.Store) *Service { return &Service{store: store} }

// SetCurrency changes the currency of the signed-in account and returns the stored row. The row is
// locked first, so a money write that sets currency_locked cannot slip in between the checks and
// the update. Saving the current code is a no-op, even for a locked account.
func (service *Service) SetCurrency(ctx context.Context, currencyCode, expectedCurrencyCode api.CurrencyCode) (gen.AppUser, error) {
	if !currencyCode.Valid() || !expectedCurrencyCode.Valid() {
		return gen.AppUser{}, apperror.BadRequest("bad_request", "currency code must be VND, USD or EUR")
	}
	owner := requestctx.Owner(ctx)
	var account gen.AppUser
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		var err error
		account, err = queries.LockUserForCurrency(ctx, owner)
		if db.IsNoRows(err) {
			return apperror.NotFound("account")
		}
		if err != nil {
			return err
		}
		switch {
		case account.CurrencyCode == string(currencyCode):
			return nil
		case account.CurrencyLocked:
			return apperror.Conflict("currency_locked", "the currency cannot change once the account records money")
		case account.CurrencyCode != string(expectedCurrencyCode):
			return apperror.Conflict("currency_mismatch", "the account currency is "+account.CurrencyCode)
		}
		account, err = queries.UpdateUserCurrency(ctx, gen.UpdateUserCurrencyParams{ID: owner, CurrencyCode: string(currencyCode)})
		return err
	})
	return account, err
}
