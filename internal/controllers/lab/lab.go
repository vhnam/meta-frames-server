package lab

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/convert"
	labsvc "meta-frames-server/internal/services/lab"
)

// Controller serves the lab endpoints.
type Controller struct {
	labs *labsvc.Service
}

// New builds the controller around its service.
func New(labs *labsvc.Service) *Controller { return &Controller{labs: labs} }

func toAPILab(view labsvc.View) api.Lab {
	lab := view.Lab
	return api.Lab{
		Id: lab.ID, Name: lab.Name, Address: lab.Address, ScannerIds: view.ScannerIDs,
		CreatedAt: convert.Timestamp(lab.CreatedAt), UpdatedAt: convert.Timestamp(lab.UpdatedAt),
	}
}

func toInput(body *api.LabInput) labsvc.Input {
	return labsvc.Input{Name: body.Name, Address: body.Address, ScannerIDs: body.ScannerIds}
}

func (controller *Controller) ListLabs(ctx context.Context, _ api.ListLabsRequestObject) (api.ListLabsResponseObject, error) {
	labs, err := controller.labs.List(ctx)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.Lab, len(labs))
	for index, lab := range labs {
		mapped[index] = toAPILab(lab)
	}
	return api.ListLabs200JSONResponse(mapped), nil
}

func (controller *Controller) GetLab(ctx context.Context, request api.GetLabRequestObject) (api.GetLabResponseObject, error) {
	lab, err := controller.labs.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetLab200JSONResponse(toAPILab(lab)), nil
}

func (controller *Controller) CreateLab(ctx context.Context, request api.CreateLabRequestObject) (api.CreateLabResponseObject, error) {
	lab, err := controller.labs.Create(ctx, uuid.New(), toInput(request.Body))
	if err != nil {
		return nil, err
	}
	return api.CreateLab201JSONResponse(toAPILab(lab)), nil
}

func (controller *Controller) PutLab(ctx context.Context, request api.PutLabRequestObject) (api.PutLabResponseObject, error) {
	lab, err := controller.labs.Update(ctx, request.Id, toInput(request.Body))
	if apperror.IsNotFound(err) {
		if lab, err = controller.labs.Create(ctx, request.Id, toInput(request.Body)); err != nil {
			return nil, err
		}
		return api.PutLab201JSONResponse(toAPILab(lab)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.PutLab200JSONResponse(toAPILab(lab)), nil
}

func (controller *Controller) DeleteLab(ctx context.Context, request api.DeleteLabRequestObject) (api.DeleteLabResponseObject, error) {
	if err := controller.labs.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteLab204Response{}, nil
}
