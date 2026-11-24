package account

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/services/account"
)

// Controller serves the account endpoints that are not authboss's own (see internal/auth).
type Controller struct{ accounts *account.Service }

func New(accounts *account.Service) *Controller { return &Controller{accounts: accounts} }

func (*Controller) GetCurrentUser(ctx context.Context, _ api.GetCurrentUserRequestObject) (api.GetCurrentUserResponseObject, error) {
	user, ok := auth.CurrentUser(ctx)
	if !ok {
		return api.GetCurrentUser401JSONResponse{Code: "unauthorized", Message: "not logged in"}, nil
	}
	return api.GetCurrentUser200JSONResponse(user.API()), nil
}

func (controller *Controller) SetCurrency(ctx context.Context, request api.SetCurrencyRequestObject) (api.SetCurrencyResponseObject, error) {
	if _, ok := auth.CurrentUser(ctx); !ok {
		return api.SetCurrency401JSONResponse{Code: "unauthorized", Message: "not logged in"}, nil
	}
	row, err := controller.accounts.SetCurrency(ctx, request.Body.CurrencyCode, request.Body.ExpectedCurrencyCode)
	if err != nil {
		return nil, err
	}
	return api.SetCurrency200JSONResponse(auth.PublicUser(row)), nil
}
