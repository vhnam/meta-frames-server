package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

var (
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 64)...)
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{2}, 64)...)
)

func TestProcessingMovesARollThroughTheLifecycle(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.finishedRoll(seeded)

	labID, jobID := newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A", "address": "HCMC"}), http.StatusCreated)

	// UC-24: process defaults from the stock; retry is idempotent; only one open job at a time.
	sent := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop_scan", "price": 90000}), http.StatusCreated)
	if sent.Body["process"] != "C-41" || sent.Body["isOpen"] != true {
		test.Fatalf("job = %s", sent.Raw)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop_scan", "price": 90000}), http.StatusOK)
	if status := harness.rollStatus(rollID); status != "at_lab" {
		test.Fatalf("status = %s, want at_lab", status)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{"type": "scan"}), http.StatusConflict)

	// UC-30 / UC-31: preview, then import.
	preview := harness.expect(harness.call("POST", "/processing/"+jobID+"/scans/preview", map[string]any{
		"fileNames": []string{"b.jpg", "a.jpg", "notes.txt"}, "startFrame": 3,
	}), http.StatusOK)
	var sequential []map[string]any
	_ = json.Unmarshal(preview.Raw, &sequential)
	if sequential[1]["frameNumber"] != float64(3) || sequential[0]["frameNumber"] != float64(4) || sequential[2]["error"] == nil {
		test.Fatalf("startFrame preview = %s", preview.Raw)
	}
	offset := harness.expect(harness.call("POST", "/processing/"+jobID+"/scans/preview", map[string]any{
		"fileNames": []string{"000010.jpg", "x.jpg"}, "offset": -8,
	}), http.StatusOK)
	var shifted []map[string]any
	_ = json.Unmarshal(offset.Raw, &shifted)
	if shifted[0]["frameNumber"] != float64(2) || shifted[1]["error"] == nil {
		test.Fatalf("offset preview = %s", offset.Raw)
	}

	imported := harness.expect(harness.uploadScans(jobID, "noritsu", "", []upload{
		{fileName: "000001.jpg", content: jpegBytes},
		{fileName: "000002.png", content: pngBytes},
		{fileName: "readme.jpg", content: []byte("plain text, not an image")},
		{fileName: "scan.jpg", frameNumber: frameNumber(7), content: jpegBytes},
	}), http.StatusOK)
	if len(asList(imported.Body["imported"])) != 3 || len(asList(imported.Body["failed"])) != 1 {
		test.Fatalf("import = %s", imported.Raw)
	}
	if imported.Body["scansReceivedAt"] == nil {
		test.Fatal("scansReceivedAt should be set by the first import")
	}

	// Scans received closes the job, so the roll is scanned; negatives are still at the lab.
	if status := harness.rollStatus(rollID); status != "scanned" {
		test.Fatalf("status = %s, want scanned", status)
	}
	if scanned := harness.listOf("/rolls?status=scanned"); len(scanned) != 1 || scanned[0]["negativesAtLab"] != true {
		test.Fatalf("scanned rolls = %v", scanned)
	}
	if atLab := harness.listOf("/negatives-at-lab"); len(atLab) != 1 || atLab[0]["labName"] != "Lab A" {
		test.Fatalf("negatives at lab = %v", atLab)
	}

	// 8a: duplicates are skipped, or replaced on request. 2a: a second scanner reuses frames.
	skipped := harness.expect(harness.uploadScans(jobID, "noritsu", "", []upload{{fileName: "000001.jpg", content: jpegBytes}}), http.StatusOK)
	if len(asList(skipped.Body["skipped"])) != 1 || len(asList(skipped.Body["imported"])) != 0 {
		test.Fatalf("skip = %s", skipped.Raw)
	}
	replaced := harness.expect(harness.uploadScans(jobID, "noritsu", "replace", []upload{{fileName: "000001.png", content: pngBytes}}), http.StatusOK)
	if len(asList(replaced.Body["imported"])) != 1 {
		test.Fatalf("replace = %s", replaced.Raw)
	}
	harness.expect(harness.uploadScans(jobID, "frontier", "", []upload{
		{fileName: "000002.jpg", content: jpegBytes}, {fileName: "000003.jpg", content: jpegBytes},
	}), http.StatusOK)
	detail := harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK)
	if frames := asList(detail.Body["frames"]); len(frames) != 4 { // 1, 2, 3, 7
		test.Fatalf("frames = %v", frames)
	}
	harness.expect(harness.uploadScans(jobID, "bogus", "", []upload{{fileName: "000001.jpg", content: jpegBytes}}), http.StatusUnprocessableEntity)

	// UC-32 / UC-33.
	grid := harness.listOf("/processing/" + jobID + "/scans?scanner=frontier")
	if len(grid) != 2 || grid[0]["frameNumber"] != float64(2) {
		test.Fatalf("grid = %v", grid)
	}
	both := harness.expect(harness.call("GET", "/processing/"+jobID+"/frames/2/compare", nil), http.StatusOK)
	if both.Body["noritsu"] == nil || both.Body["frontier"] == nil || both.Body["previousFrameNumber"] != float64(1) || both.Body["nextFrameNumber"] != float64(3) {
		test.Fatalf("compare = %s", both.Raw)
	}
	onlyNoritsu := harness.expect(harness.call("GET", "/processing/"+jobID+"/frames/1/compare", nil), http.StatusOK)
	if missing := asList(onlyNoritsu.Body["missing"]); len(missing) != 1 || missing[0] != "frontier" {
		test.Fatalf("compare missing = %s", onlyNoritsu.Raw)
	}
	harness.expect(harness.call("GET", "/processing/"+jobID+"/frames/99/compare", nil), http.StatusNotFound)

	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, httptest.NewRequest("GET", grid[0]["fileUrl"].(string), nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(recorder.Body.Bytes(), jpegBytes) {
		test.Fatalf("file download: %d %s", recorder.Code, recorder.Header().Get("Content-Type"))
	}

	// UC-34.
	noted := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/frames/2", map[string]any{"notes": "Ben Thanh market"}), http.StatusOK)
	if noted.Body["notes"] != "Ben Thanh market" || len(asList(noted.Body["scans"])) != 2 {
		test.Fatalf("frame = %s", noted.Raw)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/frames/20", map[string]any{"notes": "new"}), http.StatusOK)

	// UC-26 then UC-28: negatives back, then a rescan.
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/negatives-returned", nil), http.StatusOK)
	if scanned := harness.listOf("/rolls?status=scanned"); scanned[0]["negativesAtLab"] != false {
		test.Fatal("the badge should clear once negatives are back")
	}
	if atLab := harness.listOf("/negatives-at-lab"); len(atLab) != 0 {
		test.Fatalf("negatives at lab = %v", atLab)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{"type": "develop"}), http.StatusConflict)
	rescanID := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+rescanID, map[string]any{"type": "scan", "labId": labID, "notes": "4000dpi"}), http.StatusCreated)
	if status := harness.rollStatus(rollID); status != "at_lab" {
		test.Fatalf("resend status = %s", status)
	}
	harness.expect(harness.call("PUT", "/processing/"+rescanID+"/scans-received", map[string]any{"date": "2026-10-01"}), http.StatusOK)
	if status := harness.rollStatus(rollID); status != "scanned" {
		test.Fatalf("rescan status = %s", status)
	}
	harness.expect(harness.call("PUT", "/processing/"+rescanID+"/negatives-returned", nil), http.StatusOK)

	// UC-23 and UC-29, including totals.
	harness.expect(harness.call("DELETE", "/labs/"+labID, nil), http.StatusConflict)
	harness.expect(harness.call("PUT", "/labs/"+newID(), map[string]any{"name": " "}), http.StatusUnprocessableEntity)
	spareLabID := newID()
	harness.expect(harness.call("PUT", "/labs/"+spareLabID, map[string]any{"name": "Spare"}), http.StatusCreated)
	harness.expect(harness.call("DELETE", "/labs/"+spareLabID, nil), http.StatusNoContent)
	history := harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK)
	if jobs := asList(history.Body["processing"]); len(jobs) != 2 {
		test.Fatalf("processing history = %v", jobs)
	}
	totals := asObject(history.Body["totals"])
	if totals["processingPrice"] != float64(90000) || totals["total"] != float64(90000) || totals["incomplete"] != true {
		test.Fatalf("totals = %v", totals)
	}
}

func TestHomeProcessingNeedsNoReturnedNegatives(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.finishedRoll(seeded)

	homeJobID := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+homeJobID, map[string]any{"type": "develop"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+homeJobID+"/scans-received", nil), http.StatusConflict)
	harness.expect(harness.call("PUT", "/processing/"+homeJobID+"/negatives-returned", nil), http.StatusConflict)

	// Closing a develop-only job leaves the roll developed (never scanned).
	harness.execSQL(`UPDATE processing SET negatives_returned_at = CURRENT_DATE WHERE id = $1`, homeJobID)
	harness.execSQL(`UPDATE roll SET status = 'developed' WHERE id = $1`, rollID)
	if status := harness.rollStatus(rollID); status != "developed" {
		test.Fatalf("status = %s", status)
	}
}
