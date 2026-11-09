package roll

import (
	"slices"

	"meta-frames-server/internal/db/gen"
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
	// IfMatch, when set, makes the update conditional on the roll's current version.
	IfMatch *VersionCondition
}

// VersionCondition is a parsed If-Match header: any version, or one of a list.
type VersionCondition struct {
	Any      bool
	Versions []int64
}

// Holds reports whether a roll at version may be written.
func (condition VersionCondition) Holds(version int64) bool {
	return condition.Any || slices.Contains(condition.Versions, version)
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
