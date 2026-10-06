package processing

import (
	"meta-frames-server/internal/db/gen"
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
	Notes               *string
	// ScanOrders replaces the job's ordered scanners; nil means none.
	ScanOrders []ScanOrderInput
}

type ScanOrderInput struct {
	Scanner string
	HiRes   bool
}

type ScanOrder struct {
	Scanner   string
	HiRes     bool
	ScanCount int
}

type View struct {
	Job        gen.Processing
	LabName    string
	ScanOrders []ScanOrder
	IsOpen     bool
}
