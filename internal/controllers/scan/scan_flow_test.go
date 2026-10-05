package scan

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services"
	scansvc "meta-frames-server/internal/services/scan"
	"meta-frames-server/internal/testutil"
)

var jpeg = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 32)...)

type fixture struct {
	controller *Controller
	memory     *testutil.Memory
	rollID     uuid.UUID
	jobID      uuid.UUID
}

func newFixture(jobType string) fixture {
	memory := testutil.NewMemory()
	all := services.New(testutil.Store{Querier: memory}, testutil.NewMemoryFiles(), clock.Fixed{Date: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	rollID, jobID := uuid.New(), uuid.New()
	memory.Rolls[rollID] = gen.Roll{ID: rollID, Status: domain.RollStatusAtLab}
	memory.Jobs[jobID] = gen.Processing{ID: jobID, RollID: rollID, Type: jobType, SentAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	for _, scanner := range []string{domain.ScannerNoritsu, domain.ScannerFrontier} {
		memory.ScanOrders = append(memory.ScanOrders, gen.ProcessingScanOrder{ProcessingID: jobID, Scanner: scanner})
	}
	return fixture{controller: New(all.Scans), memory: memory, rollID: rollID, jobID: jobID}
}

type part struct {
	name, fileName string
	content        []byte
}

func multipartBody(test *testing.T, parts ...part) *multipart.Reader {
	test.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, item := range parts {
		if item.fileName == "" {
			_ = writer.WriteField(item.name, string(item.content))
			continue
		}
		file, _ := writer.CreateFormFile(item.name, item.fileName)
		_, _ = file.Write(item.content)
	}
	_ = writer.Close()
	return multipart.NewReader(&buffer, writer.Boundary())
}

func (f fixture) importParts(test *testing.T, parts ...part) (api.ImportScansResponseObject, error) {
	test.Helper()
	return f.controller.ImportScans(context.Background(), api.ImportScansRequestObject{Id: f.jobID, Body: multipartBody(test, parts...)})
}

func TestImportScansStoresFilesAndReportsFailures(test *testing.T) {
	f := newFixture(domain.JobTypeDevelopScan)
	response, err := f.importParts(test,
		part{name: "scanner", content: []byte("noritsu")},
		part{name: "file", fileName: "000001.jpg", content: jpeg},
		part{name: "file", fileName: "000001.jpg", content: jpeg}, // duplicate -> skipped
		part{name: "frameNumber", content: []byte("5")},
		part{name: "file", fileName: "scan.jpg", content: []byte("not an image")}, // rejected
		part{name: "file", fileName: "cover.jpg", content: jpeg},                  // no frame number
	)
	if err != nil {
		test.Fatal(err)
	}
	result := response.(api.ImportScans200JSONResponse)
	if len(result.Imported) != 1 || len(result.Skipped) != 1 || len(result.Failed) != 2 {
		test.Fatalf("result = %+v", result)
	}
	if !result.ScansReceivedAt.Time.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)) {
		test.Fatalf("ScansReceivedAt = %v", result.ScansReceivedAt)
	}
}

func TestImportScansValidatesTheRequest(test *testing.T) {
	f := newFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()

	for name, parts := range map[string][]part{
		"file before scanner": {{name: "file", fileName: "000001.jpg", content: jpeg}},
		"no files":            {{name: "scanner", content: []byte("noritsu")}},
		"bad scanner":         {{name: "scanner", content: []byte("epson")}},
		"no scanner at all":   {{name: "frameNumber", content: []byte("1")}},
	} {
		if _, err := f.importParts(test, parts...); err == nil {
			test.Errorf("%s: expected an error", name)
		}
	}
	if _, err := f.controller.ImportScans(ctx, api.ImportScansRequestObject{Id: f.jobID}); err == nil {
		test.Error("a missing body must be rejected")
	}
	if _, err := f.controller.ImportScans(ctx, api.ImportScansRequestObject{Id: uuid.New(), Body: multipartBody(test)}); err == nil {
		test.Error("an unknown job must fail")
	}
	broken := multipart.NewReader(bytes.NewReader([]byte("garbage")), "boundary")
	if _, err := f.controller.ImportScans(ctx, api.ImportScansRequestObject{Id: f.jobID, Body: broken}); err == nil {
		test.Error("a broken multipart body must be rejected")
	}
	noScans := newFixture(domain.JobTypeDevelop)
	if _, err := noScans.importParts(test, part{name: "scanner", content: []byte("noritsu")}); err == nil {
		test.Error("a develop-only job takes no scans")
	}
}

func TestBrowseCompareAndDownloadScans(test *testing.T) {
	f := newFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()
	if _, err := f.importParts(test,
		part{name: "scanner", content: []byte("frontier")},
		part{name: "frameNumber", content: []byte("2")},
		part{name: "file", fileName: "a.jpg", content: jpeg},
	); err != nil {
		test.Fatal(err)
	}

	scanner := api.Scanner("frontier")
	listed, err := f.controller.ListProcessingScans(ctx, api.ListProcessingScansRequestObject{Id: f.jobID, Params: api.ListProcessingScansParams{Scanner: &scanner}})
	scans := listed.(api.ListProcessingScans200JSONResponse)
	if err != nil || len(scans) != 1 {
		test.Fatalf("scans=%v err=%v", scans, err)
	}
	if _, err := f.controller.ListProcessingScans(ctx, api.ListProcessingScansRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown job must fail")
	}

	compared, err := f.controller.CompareFrame(ctx, api.CompareFrameRequestObject{Id: f.jobID, Number: 2})
	comparison := compared.(api.CompareFrame200JSONResponse)
	if err != nil || comparison.Frontier == nil || comparison.Noritsu != nil || len(comparison.Missing) != 1 {
		test.Fatalf("comparison=%+v err=%v", comparison, err)
	}
	if _, err := f.controller.CompareFrame(ctx, api.CompareFrameRequestObject{Id: f.jobID, Number: 9}); err == nil {
		test.Fatal("a frame without scans must fail")
	}

	download, err := f.controller.GetScanFile(ctx, api.GetScanFileRequestObject{Id: scans[0].Id})
	if err != nil {
		test.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := download.VisitGetScanFileResponse(recorder); err != nil || recorder.Header().Get("Content-Type") != "image/jpeg" || recorder.Body.Len() != len(jpeg) {
		test.Fatalf("headers=%v len=%d err=%v", recorder.Header(), recorder.Body.Len(), err)
	}
	if _, err := f.controller.GetScanFile(ctx, api.GetScanFileRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown scan must fail")
	}
}

func TestPutFrameAndPreviewImport(test *testing.T) {
	f := newFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()

	frame, err := f.controller.PutFrame(ctx, api.PutFrameRequestObject{Id: f.rollID, Number: 3, Body: &api.PutFrameJSONRequestBody{Notes: pointers.To("sunset")}})
	if err != nil || frame.(api.PutFrame200JSONResponse).Number != 3 {
		test.Fatalf("frame=%#v err=%v", frame, err)
	}
	if _, err := f.controller.PutFrame(ctx, api.PutFrameRequestObject{Id: uuid.New(), Number: 1, Body: &api.PutFrameJSONRequestBody{}}); err == nil {
		test.Fatal("an unknown roll must fail")
	}

	preview, err := f.controller.PreviewScanImport(ctx, api.PreviewScanImportRequestObject{Id: f.jobID, Body: &api.PreviewScanImportJSONRequestBody{FileNames: []string{"000001.jpg", "readme.txt"}}})
	items := preview.(api.PreviewScanImport200JSONResponse)
	if err != nil || len(items) != 2 || items[0].FrameNumber == nil || items[1].Error == nil {
		test.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err := f.controller.PreviewScanImport(ctx, api.PreviewScanImportRequestObject{Id: uuid.New(), Body: &api.PreviewScanImportJSONRequestBody{}}); err == nil {
		test.Fatal("an unknown job must fail")
	}
}

func TestReadFieldValueTrimsAndLimitsTheField(test *testing.T) {
	if got := readFieldValue(bytes.NewReader([]byte("  frontier \n"))); got != "frontier" {
		test.Fatalf("got %q", got)
	}
	if got := readFieldValue(bytes.NewReader(bytes.Repeat([]byte("x"), 1000))); len(got) != 256 {
		test.Fatalf("len = %d, want the 256-byte cap", len(got))
	}
}

func TestToAPIScanRefsMapsEveryRef(test *testing.T) {
	refs := ToAPIScanRefs([]scansvc.RefView{{ID: uuid.New(), FrameNumber: 4, Scanner: domain.ScannerNoritsu}})
	if len(refs) != 1 || refs[0].FrameNumber != 4 || refs[0].Scanner != api.Noritsu {
		test.Fatalf("refs = %+v", refs)
	}
}

func TestGetAndDeleteScanAndBrowseFrames(test *testing.T) {
	f := newFixture(domain.JobTypeDevelopScan)
	ctx := context.Background()
	if _, err := f.importParts(test,
		part{name: "scanner", content: []byte("noritsu")},
		part{name: "frameNumber", content: []byte("4")},
		part{name: "file", fileName: "a.jpg", content: jpeg},
	); err != nil {
		test.Fatal(err)
	}
	listed, _ := f.controller.ListProcessingScans(ctx, api.ListProcessingScansRequestObject{Id: f.jobID})
	scanID := listed.(api.ListProcessingScans200JSONResponse)[0].Id

	got, err := f.controller.GetScan(ctx, api.GetScanRequestObject{Id: scanID})
	if err != nil || got.(api.GetScan200JSONResponse).FrameNumber != 4 {
		test.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := f.controller.GetScan(ctx, api.GetScanRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown scan must fail")
	}

	frames, err := f.controller.ListRollFrames(ctx, api.ListRollFramesRequestObject{Id: f.rollID})
	if err != nil || len(frames.(api.ListRollFrames200JSONResponse)) != 1 {
		test.Fatalf("frames=%#v err=%v", frames, err)
	}
	frame, err := f.controller.GetFrame(ctx, api.GetFrameRequestObject{Id: f.rollID, Number: 4})
	if err != nil || len(frame.(api.GetFrame200JSONResponse).Scans) != 1 {
		test.Fatalf("frame=%#v err=%v", frame, err)
	}
	if _, err := f.controller.GetFrame(ctx, api.GetFrameRequestObject{Id: f.rollID, Number: 9}); err == nil {
		test.Fatal("an unknown frame must fail")
	}
	if _, err := f.controller.ListRollFrames(ctx, api.ListRollFramesRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown roll must fail")
	}

	deleted, err := f.controller.DeleteScan(ctx, api.DeleteScanRequestObject{Id: scanID})
	if _, ok := deleted.(api.DeleteScan204Response); err != nil || !ok {
		test.Fatalf("deleted=%#v err=%v", deleted, err)
	}
	if _, err := f.controller.DeleteScan(ctx, api.DeleteScanRequestObject{Id: scanID}); err == nil {
		test.Fatal("deleting a missing scan must fail")
	}
}
