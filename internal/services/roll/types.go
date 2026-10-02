package roll

import (
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services/processing"
	"meta-frames-server/internal/services/scan"
	"time"

	"github.com/google/uuid"
)

const (
	defaultExposures135 = 36
	format135           = 135
	expiryHorizonMonths = 6
	unknownExpiryMonth  = 12
)

type BulkInput struct {
	FilmStockID    uuid.UUID
	Format         int
	Exposures      *int
	Quantity       int
	Price          *int
	ExpiryYear     *int
	ExpiryMonth    *int
	IdempotencyKey string
}

type Input struct {
	FilmStockID uuid.UUID
	Format      int
	Exposures   int
	Price       *int
	ExpiryYear  *int
	ExpiryMonth *int
	ShotISO     *int
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Description *string
}

type LoadInput struct {
	CameraID  uuid.UUID
	StartedAt *time.Time
	ShotISO   *int
	// LensIDs is ignored for fixed-lens cameras; nil means "decide later".
	LensIDs *[]uuid.UUID
}

type Filter struct {
	Status      *string
	FilmStockID *uuid.UUID
	CameraID    *uuid.UUID
	LensID      *uuid.UUID
	Format      *int
	StartedFrom *time.Time
	StartedTo   *time.Time
}

type Summary struct{ gen.ListRollSummariesRow }

// ShowsNegativesAtLab is true for processed rolls whose negatives are still at a lab.
func (summary Summary) ShowsNegativesAtLab() bool {
	return summary.NegativesAtLab &&
		(summary.Status == domain.RollStatusScanned || summary.Status == domain.RollStatusDeveloped)
}

type Totals struct {
	RollPrice       int32
	ProcessingPrice int32
	Incomplete      bool
}

func (totals Totals) Total() int32 { return totals.RollPrice + totals.ProcessingPrice }

type Detail struct {
	Summary    Summary
	Stock      gen.FilmStock
	BaseStock  *gen.FilmStock
	Lenses     []gen.Lens
	Processing []processing.View
	Frames     []scan.FrameView
	Totals     Totals
	Warnings   []string
}

type ExpiringRoll struct {
	Roll      Summary
	ExpiresOn time.Time
	Expired   bool
}

type ExpiryReport struct {
	Expiring []ExpiringRoll
	NoExpiry []Summary
}
