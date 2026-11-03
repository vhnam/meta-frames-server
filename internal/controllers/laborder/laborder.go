package laborder

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	laborderservice "meta-frames-server/internal/services/laborder"
)

// Controller serves the lab order endpoints.
type Controller struct {
	orders *laborderservice.Service
}

// New builds the controller around its service.
func New(orders *laborderservice.Service) *Controller { return &Controller{orders: orders} }

func toAPIJob(job laborderservice.Job) api.LabOrderJob {
	return api.LabOrderJob{
		Id: job.ID, RollId: job.RollID, Type: api.ProcessingType(job.Type), SentAt: convert.APIDate(job.SentAt),
		StockName: job.StockName, Format: int(job.Format), Exposures: int(job.Exposures),
	}
}

func toAPIOrder(view laborderservice.View) api.LabOrder {
	order := view.Order
	items := make([]api.LabOrderItem, len(view.Items))
	for index, line := range view.Items {
		jobs := make([]api.LabOrderJob, len(line.Jobs))
		for jobIndex, job := range line.Jobs {
			jobs[jobIndex] = toAPIJob(job)
		}
		items[index] = api.LabOrderItem{
			Id: line.Item.ID, Product: line.Item.Product, Options: line.Item.Options, Quantity: int(line.Item.Quantity),
			UnitPrice: int(line.Item.UnitPrice), LineTotal: line.LineTotal(), UnitCost: int(line.UnitCost), Jobs: jobs,
		}
	}
	return api.LabOrder{
		Id: order.ID, LabId: order.LabID, LabName: view.LabName, Code: order.Code, OrderedAt: convert.Timestamp(order.OrderedAt),
		DueDate: convert.ToAPIDate(order.DueDate), Vat: int(order.Vat), Discount: int(order.Discount), Notes: order.Notes,
		Subtotal: view.Subtotal(), Total: view.Total(), Items: items,
		CreatedAt: convert.Timestamp(order.CreatedAt), UpdatedAt: convert.Timestamp(order.UpdatedAt),
	}
}

func toInput(body *api.LabOrderInput) laborderservice.Input {
	input := laborderservice.Input{
		LabID: body.LabId, Code: body.Code, OrderedAt: body.OrderedAt, DueDate: convert.FromAPIDate(body.DueDate),
		VAT: pointers.Value(body.Vat), Discount: pointers.Value(body.Discount), Notes: body.Notes,
		Items: make([]laborderservice.ItemInput, len(body.Items)),
	}
	for index, item := range body.Items {
		input.Items[index] = laborderservice.ItemInput{ID: item.Id, Product: item.Product, Options: item.Options, Quantity: item.Quantity, UnitPrice: item.UnitPrice}
	}
	return input
}

func (controller *Controller) ListLabOrders(ctx context.Context, request api.ListLabOrdersRequestObject) (api.ListLabOrdersResponseObject, error) {
	orders, err := controller.orders.List(ctx, request.Params.LabId)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.LabOrder, len(orders))
	for index, order := range orders {
		mapped[index] = toAPIOrder(order)
	}
	return api.ListLabOrders200JSONResponse(mapped), nil
}

func (controller *Controller) GetLabOrder(ctx context.Context, request api.GetLabOrderRequestObject) (api.GetLabOrderResponseObject, error) {
	order, err := controller.orders.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetLabOrder200JSONResponse(toAPIOrder(order)), nil
}

func (controller *Controller) PutLabOrder(ctx context.Context, request api.PutLabOrderRequestObject) (api.PutLabOrderResponseObject, error) {
	order, created, err := controller.orders.Save(ctx, request.Id, toInput(request.Body))
	if err != nil {
		return nil, err
	}
	if created {
		return api.PutLabOrder201JSONResponse(toAPIOrder(order)), nil
	}
	return api.PutLabOrder200JSONResponse(toAPIOrder(order)), nil
}

func (controller *Controller) DeleteLabOrder(ctx context.Context, request api.DeleteLabOrderRequestObject) (api.DeleteLabOrderResponseObject, error) {
	if err := controller.orders.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteLabOrder204Response{}, nil
}

func (controller *Controller) ListLinkableJobs(ctx context.Context, request api.ListLinkableJobsRequestObject) (api.ListLinkableJobsResponseObject, error) {
	jobs, err := controller.orders.LinkableJobs(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.LabOrderJob, len(jobs))
	for index, job := range jobs {
		mapped[index] = toAPIJob(job)
	}
	return api.ListLinkableJobs200JSONResponse(mapped), nil
}
