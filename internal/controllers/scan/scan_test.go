package scan

import (
	"bytes"
	"io"
	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	scansvc "meta-frames-server/internal/services/scan"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestToAPIScanBuildsTheFileURL(test *testing.T) {
	scanID := uuid.New()
	mapped := toAPIScan(scansvc.View{Scan: gen.Scan{ID: scanID, Scanner: domain.ScannerNoritsu, FileName: "000001.jpg", SizeBytes: 12}, FrameNumber: 1})
	if mapped.FileUrl != "/scans/"+scanID.String()+"/file" || mapped.FrameNumber != 1 || mapped.Scanner != api.Noritsu {
		test.Fatalf("mapped = %+v", mapped)
	}
}

func TestToAPIFrameKeepsScansNonNil(test *testing.T) {
	if mapped := ToAPIFrame(scansvc.FrameView{Frame: gen.Frame{Number: 3}}); mapped.Scans == nil || mapped.Number != 3 {
		test.Fatalf("frame = %+v", mapped)
	}
}

func TestImportFormReadsScannerAndConflictPolicy(test *testing.T) {
	var form importForm
	if err := form.readFormField("scanner", "frontier"); err != nil || form.scanner != "frontier" {
		test.Fatalf("scanner=%q err=%v", form.scanner, err)
	}
	if err := form.readFormField("onConflict", "replace"); err != nil || !form.replaceExisting {
		test.Fatalf("replace=%v err=%v", form.replaceExisting, err)
	}
	for _, bad := range [][2]string{{"scanner", "epson"}, {"onConflict", "overwrite"}, {"frameNumber", "x"}, {"frameNumber", "-2"}} {
		if err := (&importForm{}).readFormField(bad[0], bad[1]); err == nil {
			test.Errorf("%s=%q should be rejected", bad[0], bad[1])
		}
	}
	if err := form.readFormField("unrelated", "value"); err != nil {
		test.Fatalf("unknown fields are ignored, got %v", err)
	}
}

func TestImportFormFrameNumberPrefersTheExplicitField(test *testing.T) {
	var form importForm
	if err := form.readFormField("frameNumber", "9"); err != nil {
		test.Fatal(err)
	}
	if got, found := form.takeFrameNumber("000001.jpg"); !found || got != 9 {
		test.Fatalf("explicit number = %d, %v", got, found)
	}
	// The explicit number applies to one file only; the next file falls back to its name.
	if got, found := form.takeFrameNumber("000004.jpg"); !found || got != 4 {
		test.Fatalf("fallback number = %d, %v", got, found)
	}
	if _, found := form.takeFrameNumber("cover.jpg"); found {
		test.Fatal("a file without a number has no frame")
	}
}

func TestScanFileResponseStreamsTheImage(test *testing.T) {
	content := []byte("image-bytes")
	response := scanFileResponse{file: scansvc.ScanFile{
		Content: io.NopCloser(bytes.NewReader(content)), Size: int64(len(content)), ContentType: "image/jpeg", FileName: "000001.jpg",
	}}
	recorder := httptest.NewRecorder()
	if err := response.VisitGetScanFileResponse(recorder); err != nil {
		test.Fatal(err)
	}
	if recorder.Header().Get("Content-Type") != "image/jpeg" || recorder.Header().Get("Content-Length") != "11" {
		test.Fatalf("headers = %v", recorder.Header())
	}
	if recorder.Body.String() != "image-bytes" {
		test.Fatalf("body = %q", recorder.Body.String())
	}
}
