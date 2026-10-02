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
	Price   *int
	Notes   *string
}

type View struct {
	Job      gen.Processing
	LabName  string
	Scanners []string
	IsOpen   bool
}
