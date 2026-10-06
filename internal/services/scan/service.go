package scan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services/processing"
	"meta-frames-server/internal/services/shared"
	"meta-frames-server/internal/storage"
)

type Service struct {
	store db.Store
	files storage.Store
	clock clock.Clock
	newID func() uuid.UUID
}

func New(store db.Store, files storage.Store, appClock clock.Clock) *Service {
	return &Service{store: store, files: files, clock: appClock, newID: uuid.New}
}

// ScanFile is an opened scan image ready to stream to a client.
type ScanFile struct {
	Content     io.ReadCloser
	Size        int64
	ContentType string
	FileName    string
}

// ---- shared queries ----

func BuildFrameViews(ctx context.Context, queries gen.Querier, rollID uuid.UUID) ([]FrameView, error) {
	frames, err := queries.ListFrames(ctx, rollID)
	if err != nil {
		return nil, err
	}
	scans, err := queries.ListRollScans(ctx, rollID)
	if err != nil {
		return nil, err
	}
	scansByFrame := map[uuid.UUID][]RefView{}
	for _, scan := range scans {
		scansByFrame[scan.FrameID] = append(scansByFrame[scan.FrameID], RefView{
			ID: scan.ID, ProcessingID: scan.ProcessingID, FrameNumber: scan.FrameNumber, Scanner: scan.Scanner,
		})
	}
	views := make([]FrameView, len(frames))
	for index, frame := range frames {
		scansOfFrame := scansByFrame[frame.ID]
		if scansOfFrame == nil {
			scansOfFrame = []RefView{}
		}
		views[index] = FrameView{Frame: frame, Scans: scansOfFrame}
	}
	return views, nil
}

// ---- frames ----

// SaveFrameNotes sets notes on a frame, creating the frame when it does not exist yet (UC-34).
func (service *Service) SaveFrameNotes(ctx context.Context, rollID uuid.UUID, number int, notes *string) (FrameView, error) {
	var view FrameView
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetRollForUpdate(ctx, rollID); err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		frame, err := queries.UpsertFrame(ctx, gen.UpsertFrameParams{ID: service.newID(), RollID: rollID, Number: int32(number)})
		if err != nil {
			return err
		}
		if _, err := queries.SetFrameNotes(ctx, gen.SetFrameNotesParams{ID: frame.ID, Notes: pointers.TrimmedOrNil(notes)}); err != nil {
			return err
		}
		frames, err := BuildFrameViews(ctx, queries, rollID)
		if err != nil {
			return err
		}
		for _, candidate := range frames {
			if candidate.Frame.ID == frame.ID {
				view = candidate
			}
		}
		return nil
	})
	return view, err
}

// ---- browsing ----

func toScanView(row gen.ListProcessingScansRow) View {
	return View{
		Scan: gen.Scan{
			ID: row.ID, ProcessingID: row.ProcessingID, FrameID: row.FrameID, Scanner: row.Scanner, FileKey: row.FileKey,
			FileName: row.FileName, ContentType: row.ContentType, SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt,
		},
		FrameNumber: row.FrameNumber,
	}
}

// ListScans returns the scan grid of a job ordered by frame number (UC-32).
func (service *Service) ListScans(ctx context.Context, jobID uuid.UUID, scanner *string) ([]View, error) {
	queries := service.store.Queries()
	if _, err := queries.GetProcessing(ctx, jobID); err != nil {
		return nil, shared.NotFoundOr(err, "processing job")
	}
	rows, err := queries.ListProcessingScans(ctx, gen.ListProcessingScansParams{ProcessingID: jobID, Scanner: scanner})
	if err != nil {
		return nil, err
	}
	views := make([]View, len(rows))
	for index, row := range rows {
		views[index] = toScanView(row)
	}
	return views, nil
}

// Compare returns both scanners' scans for one frame of a job, with its neighbours (UC-33).
func (service *Service) Compare(ctx context.Context, jobID uuid.UUID, frameNumber int) (FrameComparison, error) {
	scans, err := service.ListScans(ctx, jobID, nil)
	if err != nil {
		return FrameComparison{}, err
	}
	comparison := FrameComparison{FrameNumber: frameNumber, Missing: []string{}}
	found := false
	for index := range scans {
		scan := scans[index]
		if int(scan.FrameNumber) != frameNumber {
			continue
		}
		found = true
		switch scan.Scan.Scanner {
		case domain.ScannerNoritsu:
			comparison.Noritsu = &scan
		case domain.ScannerFrontier:
			comparison.Frontier = &scan
		}
	}
	if !found {
		return FrameComparison{}, apperror.NotFound("frame")
	}
	if comparison.Noritsu == nil {
		comparison.Missing = append(comparison.Missing, domain.ScannerNoritsu)
	}
	if comparison.Frontier == nil {
		comparison.Missing = append(comparison.Missing, domain.ScannerFrontier)
	}

	numbers, err := service.store.Queries().ListJobFrameNumbers(ctx, jobID)
	if err != nil {
		return FrameComparison{}, err
	}
	comparison.PreviousFrameNumber, comparison.NextFrameNumber = neighbourFrames(numbers, frameNumber)
	return comparison, nil
}

// neighbourFrames finds the closest frame numbers below and above current (numbers is ascending).
func neighbourFrames(numbers []int32, current int) (previous, next *int) {
	for _, number := range numbers {
		switch {
		case int(number) < current:
			previous = pointers.To(int(number))
		case int(number) > current && next == nil:
			next = pointers.To(int(number))
		}
	}
	return previous, next
}

// OpenFile opens a stored scan image for download.
func (service *Service) OpenFile(ctx context.Context, scanID uuid.UUID) (ScanFile, error) {
	scan, err := service.store.Queries().GetScan(ctx, scanID)
	if err != nil {
		return ScanFile{}, shared.NotFoundOr(err, "scan")
	}
	content, size, err := service.files.Open(scan.FileKey)
	if errors.Is(err, storage.ErrNotFound) {
		return ScanFile{}, apperror.NotFound("scan file")
	}
	if err != nil {
		return ScanFile{}, err
	}
	return ScanFile{Content: content, Size: size, ContentType: scan.ContentType, FileName: scan.FileName}, nil
}

// ---- import ----

// PreviewImport maps file names to frame numbers before any upload happens.
func (service *Service) PreviewImport(ctx context.Context, jobID uuid.UUID, input ImportPreviewInput) ([]ImportPreviewItem, error) {
	if _, err := service.store.Queries().GetProcessing(ctx, jobID); err != nil {
		return nil, shared.NotFoundOr(err, "processing job")
	}
	return buildImportPreview(input), nil
}

// PrepareImport checks that the job can receive scans and returns it.
func (service *Service) PrepareImport(ctx context.Context, jobID uuid.UUID) (gen.Processing, error) {
	job, err := service.store.Queries().GetProcessing(ctx, jobID)
	if err != nil {
		return gen.Processing{}, shared.NotFoundOr(err, "processing job")
	}
	if !processing.JobProducesScans(job.Type) {
		return gen.Processing{}, apperror.Conflict("no_scans_expected", "this job type does not produce scans")
	}
	return job, nil
}

// CheckScannerOrdered rejects an import for a scanner that was not ordered on the job.
func (service *Service) CheckScannerOrdered(ctx context.Context, jobID uuid.UUID, scanner string) error {
	ordered, err := service.store.Queries().ScanOrderExists(ctx, gen.ScanOrderExistsParams{ProcessingID: jobID, Scanner: scanner})
	if err != nil {
		return err
	}
	if !ordered {
		return apperror.Unprocessable("scanner_not_ordered", "this scanner was not ordered for the job")
	}
	return nil
}

// ImportFile stores one file and records its scan. Each file commits on its own so earlier
// successes survive a later failure (UC-30 3a). A *FileRejectedError means this file alone failed.
func (service *Service) ImportFile(ctx context.Context, job gen.Processing, options ImportOptions, frameNumber int, fileName string, content io.Reader) (ImportOutcome, error) {
	buffered := bufio.NewReader(content)
	head, _ := buffered.Peek(512)
	contentType := detectImageType(head)
	if contentType == "" {
		return ImportOutcome{}, &FileRejectedError{Reason: "not a supported image (jpeg, png, webp, tiff)"}
	}

	scanID := service.newID()
	fileKey := scanID.String() + imageExtensions[contentType]
	size, err := service.files.Put(fileKey, io.LimitReader(buffered, maxScanBytes+1))
	if err != nil {
		return ImportOutcome{}, &FileRejectedError{Reason: "could not store file"}
	}
	if size > maxScanBytes {
		_ = service.files.Delete(fileKey)
		return ImportOutcome{}, &FileRejectedError{Reason: fmt.Sprintf("file exceeds %d MB", maxScanBytes>>20)}
	}

	var (
		outcome    ImportOutcome
		replacedID string
	)
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetRollForUpdate(ctx, job.RollID); err != nil {
			return err
		}
		frame, err := queries.UpsertFrame(ctx, gen.UpsertFrameParams{ID: service.newID(), RollID: job.RollID, Number: int32(frameNumber)})
		if err != nil {
			return err
		}
		existing, lookupErr := queries.GetScanSlot(ctx, gen.GetScanSlotParams{ProcessingID: job.ID, FrameID: frame.ID, Scanner: options.Scanner})
		switch {
		case lookupErr == nil && !options.ReplaceExisting:
			outcome.Skipped = true
			return nil
		case lookupErr == nil:
			replacedID = existing.FileKey
			scan, err := queries.ReplaceScan(ctx, gen.ReplaceScanParams{
				ID: existing.ID, FileKey: fileKey, FileName: fileName, ContentType: contentType, SizeBytes: size,
			})
			outcome.Scan = &View{Scan: scan, FrameNumber: frame.Number}
			return err
		case db.IsNoRows(lookupErr):
			scan, err := queries.InsertScan(ctx, gen.InsertScanParams{
				ID: scanID, ProcessingID: job.ID, FrameID: frame.ID, Scanner: options.Scanner,
				FileKey: fileKey, FileName: fileName, ContentType: contentType, SizeBytes: size,
			})
			outcome.Scan = &View{Scan: scan, FrameNumber: frame.Number}
			return err
		default:
			return lookupErr
		}
	})
	if err != nil || outcome.Skipped {
		_ = service.files.Delete(fileKey)
		if err != nil {
			return ImportOutcome{}, &FileRejectedError{Reason: "could not save scan"}
		}
		return outcome, nil
	}
	if replacedID != "" {
		_ = service.files.Delete(replacedID)
	}
	return outcome, nil
}

// CompleteImport finishes an import: if the job has no scans-received date yet and something
// was imported, it is set to today and the roll status is refreshed (UC-30 step 10).
func (service *Service) CompleteImport(ctx context.Context, jobID uuid.UUID, importedCount int) (processing.View, error) {
	var view processing.View
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		job, err := queries.GetProcessingForUpdate(ctx, jobID)
		if err != nil {
			return err
		}
		if job.ScansReceivedAt == nil && importedCount > 0 {
			if _, err := queries.GetRollForUpdate(ctx, job.RollID); err != nil {
				return err
			}
			today := service.clock.Today()
			if _, err := queries.SetScansReceived(ctx, gen.SetScansReceivedParams{ID: jobID, ScansReceivedAt: &today}); err != nil {
				return err
			}
			if err := processing.RefreshRollStatus(ctx, queries, job.RollID); err != nil {
				return err
			}
		}
		view, err = processing.BuildView(ctx, queries, jobID)
		return err
	})
	return view, err
}

// Get returns one scan's metadata.
func (service *Service) Get(ctx context.Context, scanID uuid.UUID) (View, error) {
	queries := service.store.Queries()
	scan, err := queries.GetScan(ctx, scanID)
	if err != nil {
		return View{}, shared.NotFoundOr(err, "scan")
	}
	frame, err := queries.GetFrame(ctx, scan.FrameID)
	if err != nil {
		return View{}, err
	}
	return View{Scan: scan, FrameNumber: frame.Number}, nil
}

// Delete soft-deletes a scan. The image stays in storage so the delete can be undone.
func (service *Service) Delete(ctx context.Context, scanID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetScan(ctx, scanID); err != nil {
			return shared.NotFoundOr(err, "scan")
		}
		_, err := queries.SoftDeleteScan(ctx, scanID)
		return err
	})
}

// ListFrames returns the frames of a roll that have notes or scans, ordered by number.
func (service *Service) ListFrames(ctx context.Context, rollID uuid.UUID) ([]FrameView, error) {
	queries := service.store.Queries()
	if _, err := queries.GetRoll(ctx, rollID); err != nil {
		return nil, shared.NotFoundOr(err, "roll")
	}
	return BuildFrameViews(ctx, queries, rollID)
}

// GetFrame returns one frame of a roll.
func (service *Service) GetFrame(ctx context.Context, rollID uuid.UUID, number int) (FrameView, error) {
	frames, err := service.ListFrames(ctx, rollID)
	if err != nil {
		return FrameView{}, err
	}
	for _, frame := range frames {
		if int(frame.Frame.Number) == number {
			return frame, nil
		}
	}
	return FrameView{}, apperror.NotFound("frame")
}
