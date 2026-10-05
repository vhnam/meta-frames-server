package scan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/storage"
	"meta-frames-server/internal/testutil"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func importScans(test *testing.T, fixture scanFixture, scanner string, frames ...int) {
	test.Helper()
	for _, frame := range frames {
		if _, err := fixture.importFile(ImportOptions{Scanner: scanner}, frame, "x.jpg", testJPEG); err != nil {
			test.Fatal(err)
		}
	}
}

func TestSaveFrameNotesCreatesTheFrameAndTrimsNotes(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()

	view, err := fixture.service.SaveFrameNotes(ctx, fixture.job.RollID, 5, pointers.To("  sunset "))
	if err != nil || view.Frame.Number != 5 || view.Frame.Notes == nil || *view.Frame.Notes != "sunset" {
		test.Fatalf("view=%+v err=%v", view, err)
	}
	if _, err := fixture.service.SaveFrameNotes(ctx, uuid.New(), 1, nil); err == nil {
		test.Fatal("an unknown roll must fail")
	}
}

func TestListScansAndCompareFrames(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()
	importScans(test, fixture, domain.ScannerNoritsu, 1, 2, 3)
	importScans(test, fixture, domain.ScannerFrontier, 2)

	scans, err := fixture.service.ListScans(ctx, fixture.job.ID, nil)
	if err != nil || len(scans) != 4 {
		test.Fatalf("scans=%v err=%v", scans, err)
	}
	if only, err := fixture.service.ListScans(ctx, fixture.job.ID, pointers.To(domain.ScannerFrontier)); err != nil || len(only) != 1 {
		test.Fatalf("frontier scans=%v err=%v", only, err)
	}
	if _, err := fixture.service.ListScans(ctx, uuid.New(), nil); err == nil {
		test.Fatal("an unknown job must fail")
	}

	both, err := fixture.service.Compare(ctx, fixture.job.ID, 2)
	if err != nil || both.Noritsu == nil || both.Frontier == nil || len(both.Missing) != 0 {
		test.Fatalf("both=%+v err=%v", both, err)
	}
	if both.PreviousFrameNumber == nil || *both.PreviousFrameNumber != 1 || both.NextFrameNumber == nil || *both.NextFrameNumber != 3 {
		test.Fatalf("neighbours = %v / %v", both.PreviousFrameNumber, both.NextFrameNumber)
	}
	partial, err := fixture.service.Compare(ctx, fixture.job.ID, 1)
	if err != nil || partial.Frontier != nil || len(partial.Missing) != 1 || partial.Missing[0] != domain.ScannerFrontier {
		test.Fatalf("partial=%+v err=%v", partial, err)
	}
	if _, err := fixture.service.Compare(ctx, fixture.job.ID, 9); err == nil {
		test.Fatal("a frame without scans must be not found")
	}
	if _, err := fixture.service.Compare(ctx, uuid.New(), 1); err == nil {
		test.Fatal("an unknown job must fail")
	}
}

type failingFiles struct {
	*memoryFiles
	err error
}

func (files failingFiles) Open(string) (io.ReadCloser, int64, error) { return nil, 0, files.err }

func TestOpenFileStreamsTheStoredImage(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()
	outcome, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu}, 1, "000001.jpg", testJPEG)
	if err != nil {
		test.Fatal(err)
	}

	file, err := fixture.service.OpenFile(ctx, outcome.Scan.Scan.ID)
	if err != nil || file.ContentType != "image/jpeg" || file.FileName != "000001.jpg" {
		test.Fatalf("file=%+v err=%v", file, err)
	}
	content, _ := io.ReadAll(file.Content)
	if !bytes.Equal(content, testJPEG) {
		test.Fatal("content differs")
	}

	if _, err := fixture.service.OpenFile(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown scan must fail")
	}
	fixture.service.files = failingFiles{memoryFiles: fixture.files, err: storage.ErrNotFound}
	assertAppError(test, func() error { _, err := fixture.service.OpenFile(ctx, outcome.Scan.Scan.ID); return err }(), apperror.KindNotFound, "not_found")
	fixture.service.files = failingFiles{memoryFiles: fixture.files, err: errBoom}
	if _, err := fixture.service.OpenFile(ctx, outcome.Scan.Scan.ID); !errors.Is(err, errBoom) {
		test.Fatalf("err = %v", err)
	}
}

func TestPreviewImportMapsFileNamesToFrames(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	items, err := fixture.service.PreviewImport(context.Background(), fixture.job.ID, ImportPreviewInput{FileNames: []string{"000001.jpg", "notes.txt"}})
	if err != nil || len(items) != 2 || items[0].FrameNumber == nil || *items[0].FrameNumber != 1 || items[1].Problem == "" {
		test.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err := fixture.service.PreviewImport(context.Background(), uuid.New(), ImportPreviewInput{}); err == nil {
		test.Fatal("an unknown job must fail")
	}
}

func TestFileRejectedErrorMessage(test *testing.T) {
	if got := (&FileRejectedError{Reason: "bad"}).Error(); got != "bad" {
		test.Fatalf("Error() = %q", got)
	}
}

func TestFrameNumberFromName(test *testing.T) {
	cases := []struct {
		fileName   string
		wantNumber int
		wantFound  bool
	}{
		{"000012.jpg", 12, true},
		{"DSC_0007.JPG", 7, true},
		{"roll1-frame03.png", 3, true},
		{"R0001234-05.tif", 5, true},
		{"photo_12_edit.jpg", 12, true},
		{"folder/000100.jpg", 100, true},
		{"cover.jpg", 0, false},
		{"", 0, false},
	}
	for _, testCase := range cases {
		number, found := FrameNumberFromName(testCase.fileName)
		if number != testCase.wantNumber || found != testCase.wantFound {
			test.Errorf("FrameNumberFromName(%q) = %d, %v; want %d, %v", testCase.fileName, number, found, testCase.wantNumber, testCase.wantFound)
		}
	}
}

func TestHasImageExtension(test *testing.T) {
	for _, name := range []string{"a.jpg", "a.JPEG", "a.png", "a.tif", "a.TIFF", "a.webp"} {
		if !hasImageExtension(name) {
			test.Errorf("%q should be an image", name)
		}
	}
	for _, name := range []string{"notes.txt", "scan", "archive.zip", "a.jpg.bak"} {
		if hasImageExtension(name) {
			test.Errorf("%q should not be an image", name)
		}
	}
}

func TestDetectImageType(test *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, "image/jpeg"},
		{"png", []byte("\x89PNG\r\n\x1a\nrest"), "image/png"},
		{"little-endian tiff", []byte("II*\x00rest"), "image/tiff"},
		{"big-endian tiff", []byte("MM\x00*rest"), "image/tiff"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"text", []byte("plain text, not an image"), ""},
		{"empty", nil, ""},
	}
	for _, testCase := range cases {
		if got := detectImageType(testCase.head); got != testCase.want {
			test.Errorf("%s: detectImageType = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func frameNumbers(items []ImportPreviewItem) []int {
	numbers := make([]int, len(items))
	for index, item := range items {
		numbers[index] = -1
		if item.FrameNumber != nil {
			numbers[index] = *item.FrameNumber
		}
	}
	return numbers
}

func TestBuildImportPreviewReadsNumbersFromNames(test *testing.T) {
	items := buildImportPreview(ImportPreviewInput{FileNames: []string{"000003.jpg", "000001.jpg", "cover.jpg", "notes.txt"}})
	if got, want := frameNumbers(items), []int{3, 1, -1, -1}; !reflect.DeepEqual(got, want) {
		test.Fatalf("numbers = %v, want %v", got, want)
	}
	if items[2].Problem != "no frame number in file name" || items[3].Problem != "not an image file" {
		test.Fatalf("problems = %q, %q", items[2].Problem, items[3].Problem)
	}
}

func TestBuildImportPreviewAppliesOffset(test *testing.T) {
	offset := -8
	items := buildImportPreview(ImportPreviewInput{FileNames: []string{"000010.jpg", "000005.jpg"}, Offset: &offset})
	if got, want := frameNumbers(items), []int{2, -1}; !reflect.DeepEqual(got, want) {
		test.Fatalf("numbers = %v, want %v", got, want)
	}
	if items[1].Problem != "offset makes the frame number negative" {
		test.Fatalf("problem = %q", items[1].Problem)
	}
}

func TestBuildImportPreviewNumbersSequentiallyFromStartFrame(test *testing.T) {
	start := 3
	items := buildImportPreview(ImportPreviewInput{FileNames: []string{"b.jpg", "a.jpg", "c.jpg", "notes.txt"}, StartFrame: &start})
	// Numbered in file-name order (a, b, c) but reported in input order.
	if got, want := frameNumbers(items), []int{4, 3, 5, -1}; !reflect.DeepEqual(got, want) {
		test.Fatalf("numbers = %v, want %v", got, want)
	}
}

func TestNeighbourFrames(test *testing.T) {
	numbers := []int32{1, 2, 3, 7}
	cases := []struct {
		current                int
		wantPrevious, wantNext int
		hasPrevious, hasNext   bool
	}{
		{2, 1, 3, true, true},
		{1, 0, 2, false, true},
		{7, 3, 0, true, false},
		{5, 3, 7, true, true},
	}
	for _, testCase := range cases {
		previous, next := neighbourFrames(numbers, testCase.current)
		if (previous != nil) != testCase.hasPrevious || (previous != nil && *previous != testCase.wantPrevious) {
			test.Errorf("current %d: previous = %v", testCase.current, previous)
		}
		if (next != nil) != testCase.hasNext || (next != nil && *next != testCase.wantNext) {
			test.Errorf("current %d: next = %v", testCase.current, next)
		}
	}
}

var (
	testJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 32)...)
	testPNG  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{2}, 32)...)
)

type scanFixture struct {
	queries *memoryQueries
	files   *memoryFiles
	service *Service
	job     gen.Processing
}

func newScanFixture(jobType string) scanFixture {
	queries := newMemoryQueries()
	files := newMemoryFiles()
	service := New(testutil.Store{Querier: queries}, files, fixedClock())
	rollID, jobID := uuid.New(), uuid.New()
	queries.Rolls[rollID] = gen.Roll{ID: rollID, Status: domain.RollStatusAtLab}
	job := gen.Processing{ID: jobID, RollID: rollID, Type: jobType, SentAt: day(2026, time.September, 1)}
	queries.Jobs[jobID] = job
	queries.ScanOrders = []gen.ProcessingScanOrder{{ProcessingID: jobID, Scanner: domain.ScannerNoritsu}}
	return scanFixture{queries: queries, files: files, service: service, job: job}
}

func (fixture scanFixture) importFile(options ImportOptions, frameNumber int, fileName string, content []byte) (ImportOutcome, error) {
	return fixture.service.ImportFile(context.Background(), fixture.job, options, frameNumber, fileName, bytes.NewReader(content))
}

func TestImportFileStoresTheImageAndCreatesFrameAndScan(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)

	outcome, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu}, 7, "000007.jpg", testJPEG)
	if err != nil || outcome.Skipped || outcome.Scan == nil {
		test.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if outcome.Scan.FrameNumber != 7 || outcome.Scan.Scan.ContentType != "image/jpeg" || outcome.Scan.Scan.SizeBytes != int64(len(testJPEG)) {
		test.Fatalf("scan = %+v", outcome.Scan)
	}
	if _, stored := fixture.files.Contents[outcome.Scan.Scan.FileKey]; !stored {
		test.Fatal("the image must be in storage")
	}
	if len(fixture.queries.Frames) != 1 || len(fixture.queries.Scans) != 1 {
		test.Fatalf("frames=%d scans=%d", len(fixture.queries.Frames), len(fixture.queries.Scans))
	}
}

func TestImportFileRejectsNonImagesWithoutStoringAnything(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeScan)

	_, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu}, 1, "notes.jpg", []byte("plain text"))
	var rejected *FileRejectedError
	if !errors.As(err, &rejected) {
		test.Fatalf("err = %v, want a FileRejectedError", err)
	}
	if len(fixture.files.Contents) != 0 || len(fixture.queries.Scans) != 0 {
		test.Fatalf("nothing should be stored: files=%d scans=%d", len(fixture.files.Contents), len(fixture.queries.Scans))
	}
}

func TestImportFileSkipsDuplicatesAndCleansUpTheUpload(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeScan)
	options := ImportOptions{Scanner: domain.ScannerNoritsu}
	first, err := fixture.importFile(options, 1, "000001.jpg", testJPEG)
	if err != nil {
		test.Fatal(err)
	}

	duplicate, err := fixture.importFile(options, 1, "000001.jpg", testJPEG)
	if err != nil || !duplicate.Skipped {
		test.Fatalf("duplicate = %+v err=%v", duplicate, err)
	}
	if len(fixture.files.Contents) != 1 {
		test.Fatalf("stored files = %d, want only the original", len(fixture.files.Contents))
	}
	if _, kept := fixture.files.Contents[first.Scan.Scan.FileKey]; !kept {
		test.Fatal("the original file must be kept")
	}
}

func TestImportFileReplacesExistingScansOnRequest(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeScan)
	first, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu}, 1, "000001.jpg", testJPEG)
	if err != nil {
		test.Fatal(err)
	}

	replaced, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu, ReplaceExisting: true}, 1, "000001.png", testPNG)
	if err != nil || replaced.Scan == nil || replaced.Skipped {
		test.Fatalf("replace = %+v err=%v", replaced, err)
	}
	if replaced.Scan.Scan.ContentType != "image/png" {
		test.Fatalf("content type = %s", replaced.Scan.Scan.ContentType)
	}
	if _, stillThere := fixture.files.Contents[first.Scan.Scan.FileKey]; stillThere {
		test.Fatal("the replaced file must be deleted")
	}
	if len(fixture.queries.Scans) != 1 {
		test.Fatalf("scans = %d, want 1", len(fixture.queries.Scans))
	}
}

func TestImportFileKeepsScannersApartAndReusesTheFrame(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeScan)
	for _, scanner := range []string{domain.ScannerNoritsu, domain.ScannerFrontier} {
		if _, err := fixture.importFile(ImportOptions{Scanner: scanner}, 4, "000004.jpg", testJPEG); err != nil {
			test.Fatal(err)
		}
	}
	if len(fixture.queries.Frames) != 1 || len(fixture.queries.Scans) != 2 {
		test.Fatalf("frames=%d scans=%d, want 1 and 2", len(fixture.queries.Frames), len(fixture.queries.Scans))
	}
}

func TestPrepareImportRequiresAJobThatProducesScans(test *testing.T) {
	develop := newScanFixture(domain.JobTypeDevelop)
	_, err := develop.service.PrepareImport(context.Background(), develop.job.ID)
	assertAppError(test, err, apperror.KindConflict, "no_scans_expected")

	scan := newScanFixture(domain.JobTypeScan)
	if _, err := scan.service.PrepareImport(context.Background(), scan.job.ID); err != nil {
		test.Fatalf("scan job rejected: %v", err)
	}
	_, err = scan.service.PrepareImport(context.Background(), uuid.New())
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestCompleteImportSetsTheReceivedDateOnlyWhenScansWereImported(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	view, err := fixture.service.CompleteImport(context.Background(), fixture.job.ID, 0)
	if err != nil || view.Job.ScansReceivedAt != nil {
		test.Fatalf("nothing imported: view=%+v err=%v", view.Job, err)
	}

	fixture.queries.HasReceivedScans = true
	view, err = fixture.service.CompleteImport(context.Background(), fixture.job.ID, 3)
	if err != nil {
		test.Fatal(err)
	}
	if view.Job.ScansReceivedAt == nil || !view.Job.ScansReceivedAt.Equal(day(2026, time.October, 2)) {
		test.Fatalf("ScansReceivedAt = %v, want today", view.Job.ScansReceivedAt)
	}
	if got := fixture.queries.Rolls[fixture.job.RollID].Status; got != domain.RollStatusScanned {
		test.Fatalf("roll status = %s, want scanned", got)
	}
}

func TestCompleteImportKeepsAnExistingReceivedDate(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	earlier := day(2026, time.September, 15)
	job := fixture.queries.Jobs[fixture.job.ID]
	job.ScansReceivedAt = &earlier
	fixture.queries.Jobs[fixture.job.ID] = job

	view, err := fixture.service.CompleteImport(context.Background(), fixture.job.ID, 2)
	if err != nil || !view.Job.ScansReceivedAt.Equal(earlier) {
		test.Fatalf("view=%+v err=%v", view.Job, err)
	}
}

func TestGetAndDeleteAScan(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()
	outcome, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu}, 4, "000004.jpg", testJPEG)
	if err != nil {
		test.Fatal(err)
	}
	scanID := outcome.Scan.Scan.ID

	view, err := fixture.service.Get(ctx, scanID)
	if err != nil || view.FrameNumber != 4 || view.Scan.FileName != "000004.jpg" {
		test.Fatalf("view=%+v err=%v", view, err)
	}
	if err := fixture.service.Delete(ctx, scanID); err != nil || !fixture.queries.Scans[scanID].DeletedAt.Valid {
		test.Fatalf("err=%v scan=%+v", err, fixture.queries.Scans[scanID])
	}
	if _, stored := fixture.files.Contents[outcome.Scan.Scan.FileKey]; !stored {
		test.Fatal("the image file is kept so the delete can be undone")
	}
	if _, err := fixture.service.Get(ctx, scanID); err == nil {
		test.Fatal("a deleted scan must be invisible")
	}
	assertAppError(test, fixture.service.Delete(ctx, scanID), apperror.KindNotFound, "not_found")
	if grid, _ := fixture.service.ListScans(ctx, fixture.job.ID, nil); len(grid) != 0 {
		test.Fatalf("a deleted scan must leave the grid, got %v", grid)
	}
	// The slot is free again for a fresh import.
	if again, err := fixture.importFile(ImportOptions{Scanner: domain.ScannerNoritsu}, 4, "000004.jpg", testJPEG); err != nil || again.Skipped {
		test.Fatalf("again=%+v err=%v", again, err)
	}
}

func TestListFramesAndGetFrame(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()
	if _, err := fixture.service.SaveFrameNotes(ctx, fixture.job.RollID, 3, stringRef("sunset")); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.service.SaveFrameNotes(ctx, fixture.job.RollID, 1, nil); err != nil {
		test.Fatal(err)
	}

	frames, err := fixture.service.ListFrames(ctx, fixture.job.RollID)
	if err != nil || len(frames) != 2 || frames[0].Frame.Number != 1 || frames[1].Frame.Number != 3 {
		test.Fatalf("frames=%+v err=%v", frames, err)
	}
	frame, err := fixture.service.GetFrame(ctx, fixture.job.RollID, 3)
	if err != nil || *frame.Frame.Notes != "sunset" {
		test.Fatalf("frame=%+v err=%v", frame, err)
	}
	assertAppError(test, func() error { _, err := fixture.service.GetFrame(ctx, fixture.job.RollID, 9); return err }(), apperror.KindNotFound, "not_found")
	if _, err := fixture.service.ListFrames(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown roll must fail")
	}
}

func TestCheckScannerOrdered(test *testing.T) {
	fixture := newScanFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()

	if err := fixture.service.CheckScannerOrdered(ctx, fixture.job.ID, domain.ScannerNoritsu); err != nil {
		test.Fatal(err)
	}
	err := fixture.service.CheckScannerOrdered(ctx, fixture.job.ID, domain.ScannerFrontier)
	assertAppError(test, err, apperror.KindUnprocessable, "scanner_not_ordered")
}
