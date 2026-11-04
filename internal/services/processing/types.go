package processing

import (
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"time"

	"github.com/google/uuid"
)

type Input struct {
	LabID   *uuid.UUID
	Type    string
	Process *string
	SentAt  *time.Time
	// ScansExpectedAt is when the lab expects to deliver the scans; nil means unknown.
	ScansExpectedAt *time.Time
	// NegativesExpectedAt is when the lab expects to return the negatives of a develop or develop_scan job; nil means unknown.
	NegativesExpectedAt *time.Time
	Price               *int
	// OrderItemIDs replaces the order lines the job is billed on; empty takes it off its order.
	OrderItemIDs []uuid.UUID
	Notes        *string
	// ScanOrders replaces the job's ordered scanners; nil means none.
	ScanOrders []ScanOrderInput
}

// ScanOrderInput orders a scanner. Nil Settings keep the order's current settings, or start a
// new order from the scanner's default settings.
type ScanOrderInput struct {
	ScannerID uuid.UUID
	Settings  domain.ScanSettings
}

type ScanOrder struct {
	ScannerID   uuid.UUID
	ScannerName string
	Settings    domain.ScanSettings
	ScanCount   int
}

type View struct {
	Job        gen.Processing
	LabName    string
	ScanOrders []ScanOrder
	IsOpen     bool
	// Price is what the job costs: its order lines added up when it has some, otherwise its own.
	Price *int32
	Order *OrderLink
}

// OrderLink names the order a job is billed on and its lines there.
type OrderLink struct {
	ID    uuid.UUID
	Code  string
	Items []OrderLinkItem
}

type OrderLinkItem struct {
	ID      uuid.UUID
	Product string
}
