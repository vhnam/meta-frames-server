package camera

import (
	"meta-frames-server/internal/db/gen"
	"time"

	"github.com/google/uuid"
)

type FixedLensInput struct {
	FocalLength int
	MaxAperture float64
	Brand       *string
	Model       *string
}

type Input struct {
	Brand        string
	Model        string
	Mount        *string
	Description  *string
	HasFixedLens bool
	FixedLens    *FixedLensInput
}

type LoadedRoll struct {
	RollID     uuid.UUID
	StockID    uuid.UUID
	StockBrand string
	StockName  string
	// ShotISO is nil when the roll has no shot ISO and its stock has a box ISO range.
	ShotISO    *int32
	StartedAt  *time.Time
	DaysLoaded int32
}

type View struct {
	Camera        gen.Camera
	BuiltInLensID *uuid.UUID
	LoadedRoll    *LoadedRoll
}
