package laborder

import (
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/db/gen"
)

type Input struct {
	LabID     uuid.UUID
	Code      string
	OrderedAt time.Time
	DueDate   *time.Time
	VAT       int
	Discount  int
	Notes     *string
	Items     []ItemInput
}

type ItemInput struct {
	ID        uuid.UUID
	Product   string
	Options   *string
	Quantity  int
	UnitPrice int
}

type View struct {
	Order   gen.LabOrder
	LabName string
	Items   []ItemView
}

type ItemView struct {
	Item gen.LabOrderItem
	// UnitCost is what a job on this line costs, with the order's VAT and discount shared out.
	UnitCost int32
	Jobs     []Job
}

// Job is a processing job on an order line, or one that could go on one, with the roll it works on.
type Job struct {
	ID        uuid.UUID
	RollID    uuid.UUID
	Type      string
	SentAt    time.Time
	StockName string
	Format    int32
	Exposures int32
	// OrderItemIDs are the lines of the order the job is already on; only set on a linkable job.
	OrderItemIDs []uuid.UUID
}

func (item ItemView) LineTotal() int { return int(item.Item.Quantity) * int(item.Item.UnitPrice) }

// Subtotal adds up the lines.
func (view View) Subtotal() int {
	subtotal := 0
	for _, item := range view.Items {
		subtotal += item.LineTotal()
	}
	return subtotal
}

// Total is what the lab bills: the lines plus VAT, less the discount.
func (view View) Total() int {
	return view.Subtotal() + int(view.Order.Vat) - int(view.Order.Discount)
}
