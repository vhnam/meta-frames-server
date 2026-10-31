package lab

import (
	"github.com/google/uuid"

	"meta-frames-server/internal/db/gen"
)

// Input is a lab as the account describes it. ScannerIDs are the scanners it runs, the default
// first; nil keeps the lab's scanners as they are, an empty list clears them.
type Input struct {
	Name       string
	Address    *string
	ScannerIDs *[]uuid.UUID
}

// View is a lab with the scanners it runs, the default first.
type View struct {
	Lab        gen.Lab
	ScannerIDs []uuid.UUID
}
