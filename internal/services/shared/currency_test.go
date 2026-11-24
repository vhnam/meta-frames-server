package shared

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

func guardCase(account gen.AppUser, header []string) (context.Context, *testutil.Memory) {
	owner := uuid.New()
	account.ID = owner
	memory := testutil.NewMemory()
	memory.Users[owner] = account
	ctx := requestctx.With(context.Background(), requestctx.Meta{OwnerID: owner}.WithExpectedCurrency(header))
	return ctx, memory
}

func TestGuardMoneyWriteChecksTheHeaderAndLocksOnMatch(test *testing.T) {
	cases := []struct {
		name     string
		account  gen.AppUser
		header   []string
		wantKind apperror.Kind // 0 for success
		wantCode string
		locked   bool // lock state afterwards
	}{
		{"no header, VND account, locks", gen.AppUser{CurrencyCode: "VND"}, nil, 0, "", true},
		{"matching header locks", gen.AppUser{CurrencyCode: "USD"}, []string{"USD"}, 0, "", true},
		{"locked account stays locked", gen.AppUser{CurrencyCode: "EUR", CurrencyLocked: true}, []string{"EUR"}, 0, "", true},
		{"no header, USD account", gen.AppUser{CurrencyCode: "USD"}, nil, apperror.KindConflict, "currency_mismatch", false},
		{"other code", gen.AppUser{CurrencyCode: "VND"}, []string{"EUR"}, apperror.KindConflict, "currency_mismatch", false},
		{"other code, locked", gen.AppUser{CurrencyCode: "USD", CurrencyLocked: true}, []string{"VND"}, apperror.KindConflict, "currency_mismatch", true},
		{"empty value", gen.AppUser{CurrencyCode: "VND"}, []string{""}, apperror.KindBadRequest, "bad_request", false},
		{"lower case", gen.AppUser{CurrencyCode: "VND"}, []string{"vnd"}, apperror.KindBadRequest, "bad_request", false},
		{"two lines", gen.AppUser{CurrencyCode: "VND"}, []string{"VND", "VND"}, apperror.KindBadRequest, "bad_request", false},
		{"two empty lines", gen.AppUser{CurrencyCode: "VND"}, []string{"", ""}, apperror.KindBadRequest, "bad_request", false},
	}
	for _, testCase := range cases {
		ctx, memory := guardCase(testCase.account, testCase.header)
		err := GuardMoneyWrite(ctx, memory)
		appError, isAppError := apperror.From(err)
		switch {
		case testCase.wantKind == 0 && err != nil:
			test.Errorf("%s: unexpected error %v", testCase.name, err)
		case testCase.wantKind != 0 && (!isAppError || appError.Kind != testCase.wantKind || appError.Code != testCase.wantCode):
			test.Errorf("%s: error = %v, want %s", testCase.name, err, testCase.wantCode)
		}
		if got := memory.Users[requestctx.Owner(ctx)].CurrencyLocked; got != testCase.locked {
			test.Errorf("%s: locked = %v, want %v", testCase.name, got, testCase.locked)
		}
	}
}
