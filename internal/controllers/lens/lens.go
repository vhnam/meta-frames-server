package lens

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/db/gen"
	lenssvc "meta-frames-server/internal/services/lens"
)

// Controller serves the lens endpoints.
type Controller struct {
	lenses *lenssvc.Service
}

// New builds the controller around its service.
func New(lenses *lenssvc.Service) *Controller { return &Controller{lenses: lenses} }

func toAPILens(lens gen.Lens) api.Lens {
	return api.Lens{
		Id: lens.ID, Brand: lens.Brand, Model: lens.Model, Mount: lens.Mount, Description: lens.Description,
		FocalLength: int(lens.FocalLength), MaxAperture: lens.MaxAperture,
		IsBuiltIn: lens.IsBuiltIn, IsActive: lens.IsActive,
		CreatedAt: convert.Timestamp(lens.CreatedAt), UpdatedAt: convert.Timestamp(lens.UpdatedAt),
	}
}

func ToAPILenses(lenses []gen.Lens) []api.Lens {
	mapped := make([]api.Lens, len(lenses))
	for index, lens := range lenses {
		mapped[index] = toAPILens(lens)
	}
	return mapped
}

func (controller *Controller) ListLenses(ctx context.Context, request api.ListLensesRequestObject) (api.ListLensesResponseObject, error) {
	activeOnly := request.Params.ActiveOnly != nil && *request.Params.ActiveOnly
	lenses, err := controller.lenses.List(ctx, activeOnly, request.Params.PreferMount)
	if err != nil {
		return nil, err
	}
	return api.ListLenses200JSONResponse(ToAPILenses(lenses)), nil
}

func (controller *Controller) GetLens(ctx context.Context, request api.GetLensRequestObject) (api.GetLensResponseObject, error) {
	lens, err := controller.lenses.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetLens200JSONResponse(toAPILens(lens)), nil
}

func toLensInput(body *api.LensInput) lenssvc.Input {
	return lenssvc.Input{
		Brand: body.Brand, Model: body.Model, Mount: body.Mount, Description: body.Description,
		FocalLength: body.FocalLength, MaxAperture: body.MaxAperture,
	}
}

func (controller *Controller) CreateLens(ctx context.Context, request api.CreateLensRequestObject) (api.CreateLensResponseObject, error) {
	lens, err := controller.lenses.Create(ctx, uuid.New(), toLensInput(request.Body))
	if err != nil {
		return nil, err
	}
	return api.CreateLens201JSONResponse(toAPILens(lens)), nil
}

func (controller *Controller) PutLens(ctx context.Context, request api.PutLensRequestObject) (api.PutLensResponseObject, error) {
	input := toLensInput(request.Body)
	lens, err := controller.lenses.Update(ctx, request.Id, input)
	if apperror.IsNotFound(err) {
		if lens, err = controller.lenses.Create(ctx, request.Id, input); err != nil {
			return nil, err
		}
		return api.PutLens201JSONResponse(toAPILens(lens)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.PutLens200JSONResponse(toAPILens(lens)), nil
}

func (controller *Controller) SetLensActive(ctx context.Context, request api.SetLensActiveRequestObject) (api.SetLensActiveResponseObject, error) {
	lens, err := controller.lenses.SetActive(ctx, request.Id, request.Body.IsActive)
	if err != nil {
		return nil, err
	}
	return api.SetLensActive200JSONResponse(toAPILens(lens)), nil
}

func (controller *Controller) DeleteLens(ctx context.Context, request api.DeleteLensRequestObject) (api.DeleteLensResponseObject, error) {
	if err := controller.lenses.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteLens204Response{}, nil
}
