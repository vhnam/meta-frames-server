// Package laborder keeps what a lab bills: orders, their lines, and the jobs on each line. A job
// goes on a line through the processing package, which owns the job.
package laborder

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
}

func New(store db.Store) *Service { return &Service{store: store} }

// itemsOf builds the lines of the given orders, each with its jobs, by order.
func itemsOf(ctx context.Context, queries gen.Querier, orderIDs []uuid.UUID) (map[uuid.UUID][]ItemView, error) {
	owner := requestctx.Owner(ctx)
	jobRows, err := queries.ListLabOrderJobs(ctx, gen.ListLabOrderJobsParams{OwnerID: owner, OrderIds: orderIDs})
	if err != nil {
		return nil, err
	}
	jobsByItem := make(map[uuid.UUID][]Job, len(jobRows))
	for _, row := range jobRows {
		jobsByItem[row.OrderItemID] = append(jobsByItem[row.OrderItemID], Job{
			ID: row.ID, RollID: row.RollID, Type: row.Type, SentAt: row.SentAt,
			StockName: row.StockBrand + " " + row.StockName, Format: row.Format, Exposures: row.Exposures,
		})
	}
	itemRows, err := queries.ListLabOrderItems(ctx, gen.ListLabOrderItemsParams{OwnerID: owner, OrderIds: orderIDs})
	if err != nil {
		return nil, err
	}
	byOrder := make(map[uuid.UUID][]ItemView, len(orderIDs))
	for _, row := range itemRows {
		item := gen.LabOrderItem{
			ID: row.ID, OrderID: row.OrderID, Product: row.Product, Options: row.Options,
			Quantity: row.Quantity, UnitPrice: row.UnitPrice, Position: row.Position,
		}
		byOrder[row.OrderID] = append(byOrder[row.OrderID], ItemView{Item: item, UnitCost: row.UnitCost, Jobs: jobsByItem[row.ID]})
	}
	return byOrder, nil
}

func (service *Service) List(ctx context.Context, labID *uuid.UUID) ([]View, error) {
	queries := service.store.Queries()
	rows, err := queries.ListLabOrders(ctx, gen.ListLabOrdersParams{OwnerID: requestctx.Owner(ctx), LabID: labID})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	byOrder, err := itemsOf(ctx, queries, ids)
	if err != nil {
		return nil, err
	}
	views := make([]View, len(rows))
	for index, row := range rows {
		order := gen.LabOrder{
			ID: row.ID, LabID: row.LabID, Code: row.Code, OrderedAt: row.OrderedAt, DueDate: row.DueDate, Vat: row.Vat,
			Discount: row.Discount, Notes: row.Notes, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		views[index] = View{Order: order, LabName: row.LabName, Items: byOrder[row.ID]}
	}
	return views, nil
}

func (service *Service) Get(ctx context.Context, orderID uuid.UUID) (View, error) {
	return getView(ctx, service.store.Queries(), orderID)
}

func getView(ctx context.Context, queries gen.Querier, orderID uuid.UUID) (View, error) {
	row, err := queries.GetLabOrder(ctx, gen.GetLabOrderParams{ID: orderID, OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return View{}, shared.NotFoundOr(err, "lab order")
	}
	byOrder, err := itemsOf(ctx, queries, []uuid.UUID{orderID})
	if err != nil {
		return View{}, err
	}
	order := gen.LabOrder{
		ID: row.ID, LabID: row.LabID, Code: row.Code, OrderedAt: row.OrderedAt, DueDate: row.DueDate, Vat: row.Vat,
		Discount: row.Discount, Notes: row.Notes, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return View{Order: order, LabName: row.LabName, Items: byOrder[orderID]}, nil
}

// checkInput applies the rules that need no database.
func checkInput(input Input) (string, error) {
	code := pointers.TrimmedOrNil(&input.Code)
	if code == nil {
		return "", apperror.Unprocessable("missing_fields", "code is required")
	}
	if len(input.Items) == 0 {
		return "", apperror.Unprocessable("items_required", "an order needs at least one line")
	}
	subtotal := 0
	seen := make(map[uuid.UUID]bool, len(input.Items))
	for _, item := range input.Items {
		if pointers.IsBlank(item.Product) {
			return "", apperror.Unprocessable("missing_fields", "every line needs a product")
		}
		if seen[item.ID] {
			return "", apperror.Unprocessable("duplicate_order_item", "each line id can appear only once")
		}
		seen[item.ID] = true
		subtotal += item.Quantity * item.UnitPrice
	}
	if input.Discount > subtotal+input.VAT {
		return "", apperror.Unprocessable("invalid_discount", "discount cannot exceed the subtotal plus VAT")
	}
	return *code, nil
}

// syncItems makes the stored lines equal to the given ones. A line keeps at least room for its jobs.
func syncItems(ctx context.Context, queries gen.Querier, orderID uuid.UUID, items []ItemInput) error {
	owner := requestctx.Owner(ctx)
	wanted := make(map[uuid.UUID]ItemInput, len(items))
	for _, item := range items {
		wanted[item.ID] = item
	}
	current, err := itemsOf(ctx, queries, []uuid.UUID{orderID})
	if err != nil {
		return err
	}
	for _, line := range current[orderID] {
		item, kept := wanted[line.Item.ID]
		switch {
		case kept && item.Quantity < len(line.Jobs):
			return apperror.Conflict("order_item_over_quantity", "a line cannot take fewer rolls than it already has")
		case kept:
			continue
		case len(line.Jobs) > 0:
			return apperror.Conflict("order_item_in_use", "a line with rolls cannot be removed; take the rolls off first")
		}
		if _, err := queries.DeleteLabOrderItem(ctx, gen.DeleteLabOrderItemParams{ID: line.Item.ID, OwnerID: owner}); err != nil {
			return err
		}
	}
	for position, item := range items {
		affected, err := queries.UpsertLabOrderItem(ctx, gen.UpsertLabOrderItemParams{
			ID: item.ID, OwnerID: owner, OrderID: orderID, Product: item.Product, Options: pointers.TrimmedOrNil(item.Options),
			Quantity: int32(item.Quantity), UnitPrice: int32(item.UnitPrice), Position: int32(position),
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			return apperror.Conflict("order_item_elsewhere", "a line with this id belongs to another order")
		}
	}
	return nil
}

// Save creates the order at this id or replaces it and its lines. The boolean reports a new order.
func (service *Service) Save(ctx context.Context, orderID uuid.UUID, input Input) (View, bool, error) {
	code, err := checkInput(input)
	if err != nil {
		return View{}, false, err
	}
	var (
		view    View
		created bool
	)
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		owner := requestctx.Owner(ctx)
		if _, err := queries.GetLab(ctx, gen.GetLabParams{ID: input.LabID, OwnerID: owner}); err != nil {
			if db.IsNoRows(err) {
				return apperror.Unprocessable("unknown_lab", "lab does not exist")
			}
			return err
		}
		existing, lookupErr := queries.GetLabOrderForUpdate(ctx, gen.GetLabOrderForUpdateParams{ID: orderID, OwnerID: owner})
		if lookupErr != nil && !db.IsNoRows(lookupErr) {
			return lookupErr
		}
		if lookupErr == nil {
			if existing.LabID != input.LabID {
				hasJobs, err := queries.LabOrderHasJobs(ctx, gen.LabOrderHasJobsParams{OrderID: orderID, OwnerID: owner})
				if err != nil {
					return err
				}
				if hasJobs {
					return apperror.Conflict("order_has_jobs", "the lab of an order with rolls cannot change")
				}
			}
			if _, err := queries.UpdateLabOrder(ctx, gen.UpdateLabOrderParams{
				ID: orderID, OwnerID: owner, LabID: input.LabID, Code: code, OrderedAt: pgtype.Timestamptz{Time: input.OrderedAt, Valid: true}, DueDate: input.DueDate,
				Vat: int32(input.VAT), Discount: int32(input.Discount), Notes: pointers.TrimmedOrNil(input.Notes),
			}); err != nil {
				return err
			}
		} else {
			created = true
			if _, err := queries.InsertLabOrder(ctx, gen.InsertLabOrderParams{
				ID: orderID, OwnerID: owner, LabID: input.LabID, Code: code, OrderedAt: pgtype.Timestamptz{Time: input.OrderedAt, Valid: true}, DueDate: input.DueDate,
				Vat: int32(input.VAT), Discount: int32(input.Discount), Notes: pointers.TrimmedOrNil(input.Notes),
			}); err != nil {
				return err
			}
		}
		if err := syncItems(ctx, queries, orderID, input.Items); err != nil {
			return err
		}
		view, err = getView(ctx, queries, orderID)
		return err
	})
	if db.IsUniqueViolation(err) {
		return View{}, false, apperror.Conflict("order_code_taken", "this lab already has an order with this code")
	}
	return view, created, err
}

// Delete soft-deletes an order with no jobs on it.
func (service *Service) Delete(ctx context.Context, orderID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		owner := requestctx.Owner(ctx)
		if _, err := queries.GetLabOrderForUpdate(ctx, gen.GetLabOrderForUpdateParams{ID: orderID, OwnerID: owner}); err != nil {
			return shared.NotFoundOr(err, "lab order")
		}
		hasJobs, err := queries.LabOrderHasJobs(ctx, gen.LabOrderHasJobsParams{OrderID: orderID, OwnerID: owner})
		if err != nil {
			return err
		}
		if hasJobs {
			return apperror.Conflict("order_has_jobs", "an order with rolls cannot be deleted; take the rolls off first")
		}
		_, err = queries.SoftDeleteLabOrder(ctx, gen.SoftDeleteLabOrderParams{ID: orderID, OwnerID: owner})
		return err
	})
}

// LinkableJobs lists the jobs at the order's lab that are on no order, or only on this one.
func (service *Service) LinkableJobs(ctx context.Context, orderID uuid.UUID) ([]Job, error) {
	queries := service.store.Queries()
	owner := requestctx.Owner(ctx)
	order, err := queries.GetLabOrder(ctx, gen.GetLabOrderParams{ID: orderID, OwnerID: owner})
	if err != nil {
		return nil, shared.NotFoundOr(err, "lab order")
	}
	rows, err := queries.ListLinkableJobs(ctx, gen.ListLinkableJobsParams{OwnerID: owner, LabID: &order.LabID, OrderID: orderID})
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, len(rows))
	for index, row := range rows {
		jobs[index] = Job{
			ID: row.ID, RollID: row.RollID, Type: row.Type, SentAt: row.SentAt,
			StockName: row.StockBrand + " " + row.StockName, Format: row.Format, Exposures: row.Exposures, OrderItemIDs: row.OrderItemIds,
		}
	}
	return jobs, nil
}
