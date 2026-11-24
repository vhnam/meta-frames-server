package account

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

type queries struct {
	gen.Querier
	row     gen.AppUser
	updates []gen.UpdateUserCurrencyParams
}

func (stub *queries) LockUserForCurrency(context.Context, uuid.UUID) (gen.AppUser, error) {
	return stub.row, nil
}

func (stub *queries) UpdateUserCurrency(_ context.Context, arg gen.UpdateUserCurrencyParams) (gen.AppUser, error) {
	stub.updates = append(stub.updates, arg)
	stub.row.CurrencyCode = arg.CurrencyCode
	return stub.row, nil
}

func TestSetCurrencyChecksInOrder(test *testing.T) {
	owner := uuid.New()
	ctx := requestctx.With(context.Background(), requestctx.Meta{OwnerID: owner})
	cases := []struct {
		name         string
		row          gen.AppUser
		code, expect api.CurrencyCode
		wantCode     string // error code, or "" for success
		wantWrites   int
	}{
		{"unlocked change", gen.AppUser{CurrencyCode: "VND"}, "USD", "VND", "", 1},
		{"same code, stale expectation", gen.AppUser{CurrencyCode: "USD"}, "USD", "VND", "", 0},
		{"same code, locked, stale expectation", gen.AppUser{CurrencyCode: "USD", CurrencyLocked: true}, "USD", "VND", "", 0},
		{"locked beats stale", gen.AppUser{CurrencyCode: "USD", CurrencyLocked: true}, "EUR", "VND", "currency_locked", 0},
		{"stale expectation", gen.AppUser{CurrencyCode: "USD"}, "EUR", "VND", "currency_mismatch", 0},
		{"invalid code", gen.AppUser{CurrencyCode: "VND"}, "usd", "VND", "bad_request", 0},
	}
	for _, testCase := range cases {
		stub := &queries{row: testCase.row}
		_, err := New(testutil.Store{Querier: stub}).SetCurrency(ctx, testCase.code, testCase.expect)
		appError, isAppError := apperror.From(err)
		switch {
		case testCase.wantCode == "" && err != nil:
			test.Errorf("%s: unexpected error %v", testCase.name, err)
		case testCase.wantCode != "" && (!isAppError || appError.Code != testCase.wantCode):
			test.Errorf("%s: error = %v, want %s", testCase.name, err, testCase.wantCode)
		}
		if len(stub.updates) != testCase.wantWrites {
			test.Errorf("%s: writes = %d, want %d", testCase.name, len(stub.updates), testCase.wantWrites)
		}
	}
}
