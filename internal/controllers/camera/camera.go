package camera

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/controllers/lens"
	camerasvc "meta-frames-server/internal/services/camera"
)

// Controller serves the camera endpoints.
type Controller struct {
	cameras *camerasvc.Service
}

// New builds the controller around its service.
func New(cameras *camerasvc.Service) *Controller { return &Controller{cameras: cameras} }

func toAPICamera(view camerasvc.View) api.Camera {
	camera := api.Camera{
		Id: view.Camera.ID, Brand: view.Camera.Brand, Model: view.Camera.Model, Mount: view.Camera.Mount,
		Description: view.Camera.Description, HasFixedLens: view.Camera.HasFixedLens, IsActive: view.Camera.IsActive,
		BuiltInLensId: view.BuiltInLensID,
		CreatedAt:     convert.Timestamp(view.Camera.CreatedAt), UpdatedAt: convert.Timestamp(view.Camera.UpdatedAt),
	}
	if view.LoadedRoll != nil {
		loaded := view.LoadedRoll
		shotISO := int(loaded.ShotISO)
		camera.LoadedRoll = &api.LoadedRoll{
			RollId: loaded.RollID, StockId: loaded.StockID, StockBrand: loaded.StockBrand, StockName: loaded.StockName,
			ShotIso: &shotISO, StartedAt: convert.ToAPIDate(loaded.StartedAt), DaysLoaded: int(loaded.DaysLoaded),
		}
	}
	return camera
}

func (controller *Controller) ListCameras(ctx context.Context, request api.ListCamerasRequestObject) (api.ListCamerasResponseObject, error) {
	activeOnly := request.Params.ActiveOnly != nil && *request.Params.ActiveOnly
	views, err := controller.cameras.List(ctx, activeOnly)
	if err != nil {
		return nil, err
	}
	cameras := make([]api.Camera, len(views))
	for index, view := range views {
		cameras[index] = toAPICamera(view)
	}
	return api.ListCameras200JSONResponse(cameras), nil
}

func (controller *Controller) GetCamera(ctx context.Context, request api.GetCameraRequestObject) (api.GetCameraResponseObject, error) {
	view, err := controller.cameras.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetCamera200JSONResponse(toAPICamera(view)), nil
}

func toCameraInput(body *api.CameraInput) camerasvc.Input {
	input := camerasvc.Input{
		Brand: body.Brand, Model: body.Model, Mount: body.Mount, Description: body.Description, HasFixedLens: body.HasFixedLens,
	}
	if body.FixedLens != nil {
		input.FixedLens = &camerasvc.FixedLensInput{
			FocalLength: body.FixedLens.FocalLength, MaxAperture: body.FixedLens.MaxAperture,
			Brand: body.FixedLens.Brand, Model: body.FixedLens.Model,
		}
	}
	return input
}

func (controller *Controller) CreateCamera(ctx context.Context, request api.CreateCameraRequestObject) (api.CreateCameraResponseObject, error) {
	view, err := controller.cameras.Create(ctx, uuid.New(), toCameraInput(request.Body))
	if err != nil {
		return nil, err
	}
	return api.CreateCamera201JSONResponse(toAPICamera(view)), nil
}

func (controller *Controller) PutCamera(ctx context.Context, request api.PutCameraRequestObject) (api.PutCameraResponseObject, error) {
	input := toCameraInput(request.Body)
	view, err := controller.cameras.Update(ctx, request.Id, input)
	if apperror.IsNotFound(err) {
		if view, err = controller.cameras.Create(ctx, request.Id, input); err != nil {
			return nil, err
		}
		return api.PutCamera201JSONResponse(toAPICamera(view)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.PutCamera200JSONResponse(toAPICamera(view)), nil
}

func (controller *Controller) SetCameraActive(ctx context.Context, request api.SetCameraActiveRequestObject) (api.SetCameraActiveResponseObject, error) {
	view, err := controller.cameras.SetActive(ctx, request.Id, request.Body.IsActive)
	if err != nil {
		return nil, err
	}
	return api.SetCameraActive200JSONResponse(toAPICamera(view)), nil
}

func (controller *Controller) ListCameraLenses(ctx context.Context, request api.ListCameraLensesRequestObject) (api.ListCameraLensesResponseObject, error) {
	lenses, err := controller.cameras.ListLenses(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.ListCameraLenses200JSONResponse(lens.ToAPILenses(lenses)), nil
}

func (controller *Controller) SetCameraLenses(ctx context.Context, request api.SetCameraLensesRequestObject) (api.SetCameraLensesResponseObject, error) {
	lenses, err := controller.cameras.SetLenses(ctx, request.Id, request.Body.LensIds)
	if err != nil {
		return nil, err
	}
	return api.SetCameraLenses200JSONResponse(lens.ToAPILenses(lenses)), nil
}

func (controller *Controller) DeleteCamera(ctx context.Context, request api.DeleteCameraRequestObject) (api.DeleteCameraResponseObject, error) {
	if err := controller.cameras.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteCamera204Response{}, nil
}
