package processing

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/domain"
	processingsvc "meta-frames-server/internal/services/processing"
	scannersvc "meta-frames-server/internal/services/scanner"
)

// Controller serves the processing endpoints.
type Controller struct {
	processing *processingsvc.Service
	scanners   *scannersvc.Service
}

// New builds the controller around its services; scanners resolves the scanners a job orders.
func New(processing *processingsvc.Service, scanners *scannersvc.Service) *Controller {
	return &Controller{processing: processing, scanners: scanners}
}

func ToAPIProcessing(view processingsvc.View) api.Processing {
	job := view.Job
	mapped := api.Processing{
		Id: job.ID, RollId: job.RollID, LabId: job.LabID, Type: api.ProcessingType(job.Type), Process: api.Process(job.Process),
		SentAt: convert.APIDate(job.SentAt), ScansReceivedAt: convert.ToAPIDate(job.ScansReceivedAt),
		ScansExpectedAt:     convert.ToAPIDate(job.ScansExpectedAt),
		NegativesExpectedAt: convert.ToAPIDate(job.NegativesExpectedAt),
		NegativesReturnedAt: convert.ToAPIDate(job.NegativesReturnedAt), Price: pointers.Int(view.Price), Notes: job.Notes,
		ScanOrders: make([]api.ScanOrder, len(view.ScanOrders)), IsOpen: view.IsOpen,
	}
	if view.LabName != "" {
		mapped.LabName = pointers.To(view.LabName)
	}
	if view.Order != nil {
		mapped.Order = &api.ProcessingOrder{Id: view.Order.ID, Code: view.Order.Code, ItemId: view.Order.ItemID, Product: view.Order.Product}
	}
	for index, order := range view.ScanOrders {
		mapped.ScanOrders[index] = api.ScanOrder{
			Scanner: api.ScannerCode(domain.ScannerCode(order.ScannerID)), ScannerId: order.ScannerID, ScannerName: order.ScannerName,
			Settings: api.ScanSettings(order.Settings), HiRes: domain.IsHiRes(order.Settings), ScanCount: order.ScanCount,
		}
	}
	return mapped
}

// orderSettings reads the settings of an ordered scanner; the old hiRes flag stands for a resolution.
// Nil leaves them to the service: kept on an order already on the job, the scanner's defaults on a new one.
func orderSettings(order api.ScanOrderInput) domain.ScanSettings {
	switch {
	case order.Settings != nil:
		return domain.ScanSettings(*order.Settings)
	case order.HiRes == nil:
		return nil
	case *order.HiRes:
		return domain.ScanSettings{"resolution": domain.ResolutionHiRes}
	default:
		return domain.ScanSettings{"resolution": domain.ResolutionStandard}
	}
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
		ScansExpectedAt: convert.FromAPIDate(body.ScansExpectedAt), NegativesExpectedAt: convert.FromAPIDate(body.NegativesExpectedAt), Price: body.Price, OrderItemID: body.OrderItemId, Notes: body.Notes,
	}
	if body.Process != nil {
		input.Process = pointers.To(string(*body.Process))
	}
	if body.ScanOrders != nil {
		for _, order := range *body.ScanOrders {
			var code *string
			if order.Scanner != nil {
				code = pointers.To(string(*order.Scanner))
			}
			scannerID, err := controller.scanners.PickForOrder(ctx, order.ScannerId, code)
			if err != nil {
				return nil, err
			}
			input.ScanOrders = append(input.ScanOrders, processingsvc.ScanOrderInput{ScannerID: scannerID, Settings: orderSettings(order)})
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

func (controller *Controller) SetProcessingOrderItem(ctx context.Context, request api.SetProcessingOrderItemRequestObject) (api.SetProcessingOrderItemResponseObject, error) {
	view, err := controller.processing.SetOrderItem(ctx, request.Id, &request.Body.OrderItemId)
	if err != nil {
		return nil, err
	}
	return api.SetProcessingOrderItem200JSONResponse(ToAPIProcessing(view)), nil
}

func (controller *Controller) ClearProcessingOrderItem(ctx context.Context, request api.ClearProcessingOrderItemRequestObject) (api.ClearProcessingOrderItemResponseObject, error) {
	view, err := controller.processing.SetOrderItem(ctx, request.Id, nil)
	if err != nil {
		return nil, err
	}
	return api.ClearProcessingOrderItem200JSONResponse(ToAPIProcessing(view)), nil
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
