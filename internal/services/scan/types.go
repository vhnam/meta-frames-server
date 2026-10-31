package scan

import (
	"meta-frames-server/internal/db/gen"

	"github.com/google/uuid"
)

const maxScanBytes = 40 << 20

type ImportOptions struct {
	Scanner         gen.Scanner
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
	ScannerID    uuid.UUID
	ScannerName  string
}

type FrameView struct {
	Frame gen.Frame
	Scans []RefView
}

type View struct {
	Scan        gen.Scan
	FrameNumber int32
	ScannerName string
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

// FrameComparison holds every scan of one frame in a job. Noritsu, Frontier and Missing are
// the built-in pair the API used to compare, kept for old clients.
type FrameComparison struct {
	FrameNumber         int
	Scans               []View
	Noritsu             *View
	Frontier            *View
	Missing             []string
	PreviousFrameNumber *int
	NextFrameNumber     *int
}

// FileRejectedError explains why one uploaded file could not be imported.
type FileRejectedError struct{ Reason string }

func (rejected *FileRejectedError) Error() string { return rejected.Reason }
