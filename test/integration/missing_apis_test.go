package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestDeletingRollsJobsAndScans(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)

	// A roll still in stock can be deleted; one that was loaded cannot.
	stocked := harness.addRolls(seeded, 2)
	harness.expect(harness.call("DELETE", "/rolls/"+stocked[0], nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/rolls/"+stocked[0], nil), http.StatusNotFound)
	harness.expect(harness.call("DELETE", "/rolls/"+stocked[0], nil), http.StatusNotFound)
	harness.expect(harness.call("PUT", "/rolls/"+stocked[1]+"/load", map[string]any{"cameraId": seeded.slrCamera}), http.StatusOK)
	harness.expect(harness.call("DELETE", "/rolls/"+stocked[1], nil), http.StatusConflict)
	if rolls := harness.listOf("/rolls"); len(rolls) != 1 {
		test.Fatalf("a deleted roll must not be listed: %v", rolls)
	}

	// Processing job with a scan.
	rollID := harness.finishedRoll(seeded)
	jobID := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"type": "develop_scan", "scanOrders": []map[string]any{{"scanner": "noritsu"}}}), http.StatusCreated)
	imported := harness.expect(harness.uploadScans(jobID, "noritsu", "", []upload{{fileName: "000004.jpg", content: jpegBytes}}), http.StatusOK)
	scanID, _ := asObject(asList(imported.Body["imported"])[0])["id"].(string)

	scan := harness.expect(harness.call("GET", "/scans/"+scanID, nil), http.StatusOK)
	if scan.Body["frameNumber"] != float64(4) || scan.Body["scanner"] != "noritsu" {
		test.Fatalf("scan = %s", scan.Raw)
	}
	frames := harness.listOf("/rolls/" + rollID + "/frames")
	if len(frames) != 1 || frames[0]["number"] != float64(4) {
		test.Fatalf("frames = %v", frames)
	}
	frame := harness.expect(harness.call("GET", "/rolls/"+rollID+"/frames/4", nil), http.StatusOK)
	if len(asList(frame.Body["scans"])) != 1 {
		test.Fatalf("frame = %s", frame.Raw)
	}
	harness.expect(harness.call("GET", "/rolls/"+rollID+"/frames/99", nil), http.StatusNotFound)

	// A job with scans is protected until its scans are deleted.
	harness.expect(harness.call("DELETE", "/processing/"+jobID, nil), http.StatusConflict)
	harness.expect(harness.call("DELETE", "/scans/"+scanID, nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/scans/"+scanID, nil), http.StatusNotFound)
	harness.expect(harness.call("GET", "/scans/"+scanID+"/file", nil), http.StatusNotFound)
	harness.expect(harness.call("DELETE", "/scans/"+scanID, nil), http.StatusNotFound)
	if grid := harness.listOf("/processing/" + jobID + "/scans"); len(grid) != 0 {
		test.Fatalf("a deleted scan must leave the grid: %v", grid)
	}

	// The slot is free again (partial unique index), and the new scan is a new row.
	again := harness.expect(harness.uploadScans(jobID, "noritsu", "", []upload{{fileName: "000004.jpg", content: jpegBytes}}), http.StatusOK)
	newScanID, _ := asObject(asList(again.Body["imported"])[0])["id"].(string)
	if newScanID == "" || newScanID == scanID {
		test.Fatalf("re-import = %s", again.Raw)
	}
	harness.expect(harness.call("DELETE", "/scans/"+newScanID, nil), http.StatusNoContent)

	// With no scans left the job can go; the roll falls back to done_shooting.
	harness.expect(harness.call("DELETE", "/processing/"+jobID, nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/processing/"+jobID, nil), http.StatusNotFound)
	if status := harness.rollStatus(rollID); status != "done_shooting" {
		test.Fatalf("status = %s, want done_shooting", status)
	}
	if jobs := harness.listOf("/rolls/" + rollID + "/processing"); len(jobs) != 0 {
		test.Fatalf("jobs = %v", jobs)
	}
	harness.expect(harness.call("DELETE", "/processing/"+jobID, nil), http.StatusNotFound)
}

func TestAuditEntryByID(test *testing.T) {
	harness := newHarness(test)
	harness.expect(harness.callAs("nam", "POST", "/labs", map[string]any{"name": "Lab A"}), http.StatusCreated)

	entries := harness.auditTrail("entityType=lab")
	if len(entries) != 1 {
		test.Fatalf("entries = %v", entries)
	}
	id := int(entries[0]["id"].(float64))
	entry := harness.expect(harness.call("GET", "/audit-logs/"+itoa(id), nil), http.StatusOK)
	if entry.Body["entityType"] != "lab" || entry.Body["actor"] != "nam" {
		test.Fatalf("entry = %s", entry.Raw)
	}
	harness.expect(harness.call("GET", "/audit-logs/999999", nil), http.StatusNotFound)
	harness.expect(harness.call("GET", "/audit-logs/abc", nil), http.StatusBadRequest)
}

func itoa(value int) string { return fmt.Sprint(value) }
