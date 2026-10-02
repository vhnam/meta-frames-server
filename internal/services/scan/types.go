package scan

import (
	"meta-frames-server/internal/db/gen"

	"github.com/google/uuid"
)

const maxScanBytes = 40 << 20

type ImportOptions struct {
	Scanner         string
	ReplaceExisting bool
}

type ImportPreviewInput struct {
	FileNames  []string
	StartFrame *int
	Offset     *int
}

type RefView struct {
	ID           uuid.UUID
	ProcessingID uuid.UUID
	FrameNumber  int32
	Scanner      string
}

type FrameView struct {
	Frame gen.Frame
	Scans []RefView
}

type View struct {
	Scan        gen.Scan
	FrameNumber int32
}

type ImportPreviewItem struct {
	FileName    string
	FrameNumber *int
	Problem     string
}

type ImportOutcome struct {
	Scan    *View
	Skipped bool
}

type FrameComparison struct {
	FrameNumber         int
	Noritsu             *View
	Frontier            *View
	Missing             []string
	PreviousFrameNumber *int
	NextFrameNumber     *int
}

// FileRejectedError explains why one uploaded file could not be imported.
type FileRejectedError struct{ Reason string }

func (rejected *FileRejectedError) Error() string { return rejected.Reason }
