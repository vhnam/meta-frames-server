package processing

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	processingsvc "meta-frames-server/internal/services/processing"
)

// Controller serves the processing endpoints.
type Controller struct {
	processing *processingsvc.Service
}

// New builds the controller around its service.
func New(processing *processingsvc.Service) *Controller {
	return &Controller{processing: processing}
}

func ToAPIProcessing(view processingsvc.View) api.Processing {
	job := view.Job
	mapped := api.Processing{
		Id: job.ID, RollId: job.RollID, LabId: job.LabID, Type: api.ProcessingType(job.Type), Process: api.Process(job.Process),
		SentAt: convert.APIDate(job.SentAt), ScansReceivedAt: convert.ToAPIDate(job.ScansReceivedAt),
		ScansExpectedAt:     convert.ToAPIDate(job.ScansExpectedAt),
		NegativesExpectedAt: convert.ToAPIDate(job.NegativesExpectedAt),
		NegativesReturnedAt: convert.ToAPIDate(job.NegativesReturnedAt), Price: pointers.Int(job.Price), Notes: job.Notes,
		ScanOrders: make([]api.ScanOrder, len(view.ScanOrders)), IsOpen: view.IsOpen,
	}
	if view.LabName != "" {
		mapped.LabName = pointers.To(view.LabName)
	}
	for index, order := range view.ScanOrders {
		mapped.ScanOrders[index] = api.ScanOrder{Scanner: api.Scanner(order.Scanner), HiRes: order.HiRes, ScanCount: order.ScanCount}
	}
	return mapped
}

func (controller *Controller) ListRollProcessing(ctx context.Context, request api.ListRollProcessingRequestObject) (api.ListRollProcessingResponseObject, error) {
	views, err := controller.processing.ListForRoll(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.Processing, len(views))
	for index, view := range views {
		mapped[index] = ToAPIProcessing(view)
	}
	return api.ListRollProcessing200JSONResponse(mapped), nil
}

func (controller *Controller) GetProcessing(ctx context.Context, request api.GetProcessingRequestObject) (api.GetProcessingResponseObject, error) {
	view, err := controller.processing.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetProcessing200JSONResponse(ToAPIProcessing(view)), nil
}

func (controller *Controller) PutProcessing(ctx context.Context, request api.PutProcessingRequestObject) (api.PutProcessingResponseObject, error) {
	body := request.Body
	input := processingsvc.Input{
		LabID: body.LabId, Type: string(body.Type), SentAt: convert.FromAPIDate(body.SentAt),
		ScansExpectedAt: convert.FromAPIDate(body.ScansExpectedAt), NegativesExpectedAt: convert.FromAPIDate(body.NegativesExpectedAt), Price: body.Price, Notes: body.Notes,
	}
	if body.Process != nil {
		input.Process = pointers.To(string(*body.Process))
	}
	if body.ScanOrders != nil {
		for _, order := range *body.ScanOrders {
			input.ScanOrders = append(input.ScanOrders, processingsvc.ScanOrderInput{
				Scanner: string(order.Scanner), HiRes: order.HiRes != nil && *order.HiRes,
			})
		}
	}
	view, created, err := controller.processing.Save(ctx, request.Id, request.JobId, input)
	if err != nil {
		return nil, err
	}
	if created {
		return api.PutProcessing201JSONResponse(ToAPIProcessing(view)), nil
	}
	return api.PutProcessing200JSONResponse(ToAPIProcessing(view)), nil
}

func (controller *Controller) RecordScansReceived(ctx context.Context, request api.RecordScansReceivedRequestObject) (api.RecordScansReceivedResponseObject, error) {
	view, err := controller.processing.RecordScansReceived(ctx, request.Id, convert.OptionalDate(request.Body))
	if err != nil {
		return nil, err
	}
	return api.RecordScansReceived200JSONResponse(ToAPIProcessing(view)), nil
}

func (controller *Controller) RecordNegativesReturned(ctx context.Context, request api.RecordNegativesReturnedRequestObject) (api.RecordNegativesReturnedResponseObject, error) {
	view, err := controller.processing.RecordNegativesReturned(ctx, request.Id, convert.OptionalDate(request.Body))
	if err != nil {
		return nil, err
	}
	return api.RecordNegativesReturned200JSONResponse(ToAPIProcessing(view)), nil
}

func (controller *Controller) ListNegativesAtLab(ctx context.Context, _ api.ListNegativesAtLabRequestObject) (api.ListNegativesAtLabResponseObject, error) {
	rows, err := controller.processing.NegativesAtLab(ctx)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.NegativesAtLabItem, len(rows))
	for index, row := range rows {
		mapped[index] = api.NegativesAtLabItem{
			ProcessingId: row.ID, RollId: row.RollID, LabName: row.LabName, Type: api.ProcessingType(row.Type),
			SentAt: convert.APIDate(row.SentAt), DaysSinceSent: int(row.DaysSinceSent), StockName: row.StockBrand + " " + row.StockName,
		}
	}
	return api.ListNegativesAtLab200JSONResponse(mapped), nil
}

func (controller *Controller) DeleteProcessing(ctx context.Context, request api.DeleteProcessingRequestObject) (api.DeleteProcessingResponseObject, error) {
	if err := controller.processing.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteProcessing204Response{}, nil
}
