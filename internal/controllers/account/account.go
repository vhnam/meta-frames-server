package account

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/auth"
)

// Controller serves the account endpoints that are not authboss's own (see internal/auth).
type Controller struct{}

func New() *Controller { return &Controller{} }

func (*Controller) GetCurrentUser(ctx context.Context, _ api.GetCurrentUserRequestObject) (api.GetCurrentUserResponseObject, error) {
	user, ok := auth.CurrentUser(ctx)
	if !ok {
		return api.GetCurrentUser401JSONResponse{Code: "unauthorized", Message: "not logged in"}, nil
	}
	return api.GetCurrentUser200JSONResponse(user.API()), nil
}
