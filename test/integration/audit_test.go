package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// callAs sends a JSON request signed in as the named user.
func (harness *harness) callAs(actor, method, path string, body any) response {
	harness.test.Helper()
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(harness.sessionOf(actor))
	return harness.serve(request)
}

func (harness *harness) auditTrail(query string) []map[string]any {
	harness.test.Helper()
	return harness.listOf("/audit-logs?" + query)
}

// auditTrailOf reads the named user's audit trail; each account only sees its own.
func (harness *harness) auditTrailOf(name, query string) []map[string]any {
	harness.test.Helper()
	result := harness.expect(harness.callAs(name, "GET", "/audit-logs?"+query, nil), http.StatusOK)
	var items []map[string]any
	_ = json.Unmarshal(result.Raw, &items)
	return items
}

func TestEveryChangeIsAuditedWithActorAndRequest(test *testing.T) {
	harness := newHarness(test)
	camera := map[string]any{"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": false}

	created := harness.expect(harness.callAs("nam", "POST", "/cameras", camera), http.StatusCreated)
	id, _ := created.Body["id"].(string)
	camera["model"] = "FM3A"
	harness.expect(harness.callAs("nam", "PUT", "/cameras/"+id, camera), http.StatusOK)
	harness.expect(harness.callAs("nam", "PUT", "/cameras/"+id, camera), http.StatusOK) // identical: nothing changed
	harness.expect(harness.callAs("nam", "DELETE", "/cameras/"+id, nil), http.StatusNoContent)

	trail := harness.auditTrailOf("nam", "entityType=camera&entityId="+id)
	if len(trail) != 3 {
		test.Fatalf("want create, update, delete (a no-op update is not logged), got %d entries: %v", len(trail), trail)
	}
	// Newest first.
	deleted, updated, createdEntry := trail[0], trail[1], trail[2]
	if deleted["action"] != "delete" || deleted["actor"] != actorEmail("nam") || asObject(deleted["after"])["deleted_at"] == nil {
		test.Fatalf("delete entry = %v", deleted)
	}
	if updated["action"] != "update" || updated["actor"] != actorEmail("nam") ||
		asObject(updated["before"])["model"] != "FM2" || asObject(updated["after"])["model"] != "FM3A" {
		test.Fatalf("update entry = %v", updated)
	}
	if createdEntry["action"] != "create" || createdEntry["actor"] != actorEmail("nam") || createdEntry["before"] != nil || asObject(createdEntry["after"])["model"] != "FM2" {
		test.Fatalf("create entry = %v", createdEntry)
	}
	if requestID, _ := createdEntry["requestId"].(string); requestID == "" {
		test.Fatalf("every entry carries the request id: %v", createdEntry)
	}
	if updated["requestId"] == createdEntry["requestId"] {
		test.Fatal("different requests must have different request ids")
	}

	if deletes := harness.auditTrailOf("nam", "entityType=camera&action=delete"); len(deletes) != 1 {
		test.Fatalf("filter by action: %v", deletes)
	}
	if page := harness.auditTrailOf("nam", "entityId="+id+"&limit=1&offset=1"); len(page) != 1 || page[0]["action"] != "update" {
		test.Fatalf("paging: %v", page)
	}
	harness.expect(harness.call("GET", "/audit-logs?limit=0", nil), http.StatusBadRequest)
	harness.expect(harness.call("GET", "/audit-logs?entityType=banana", nil), http.StatusBadRequest)
}

func TestSoftDeleteKeepsTheRowAndTimestampsTrackChanges(test *testing.T) {
	harness := newHarness(test)
	labID := newID()
	first := harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	createdAt, _ := time.Parse(time.RFC3339Nano, first.Body["createdAt"].(string))
	if createdAt.IsZero() || first.Body["updatedAt"] == nil {
		test.Fatalf("a lab must carry createdAt and updatedAt: %s", first.Raw)
	}

	time.Sleep(20 * time.Millisecond)
	second := harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab B"}), http.StatusOK)
	secondCreated, _ := time.Parse(time.RFC3339Nano, second.Body["createdAt"].(string))
	updatedAt, _ := time.Parse(time.RFC3339Nano, second.Body["updatedAt"].(string))
	if !secondCreated.Equal(createdAt) || !updatedAt.After(createdAt) {
		test.Fatalf("createdAt must stay put and updatedAt must move: created %v -> %v, updated %v", createdAt, secondCreated, updatedAt)
	}

	harness.expect(harness.call("DELETE", "/labs/"+labID, nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/labs/"+labID, nil), http.StatusNotFound)
	if labs := harness.listOf("/labs"); len(labs) != 0 {
		test.Fatalf("a deleted lab must not be listed: %v", labs)
	}
	var deletedAt *time.Time
	if err := harness.pool.QueryRow(context.Background(), "SELECT deleted_at FROM lab WHERE id = $1", labID).Scan(&deletedAt); err != nil || deletedAt == nil {
		test.Fatalf("the row must remain with deleted_at set: %v %v", deletedAt, err)
	}
	// The id stays reserved: re-creating it is a conflict, not a silent resurrection.
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab C"}), http.StatusConflict)
}

func TestLinkChangesAndRollEventsAreAudited(test *testing.T) {
	harness := newHarness(test)
	cameraID, lensID := newID(), newID()
	harness.expect(harness.call("PUT", "/cameras/"+cameraID, map[string]any{"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": false}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lenses/"+lensID, map[string]any{"brand": "Nikon", "model": "50", "mount": "F", "focalLength": 50, "maxAperture": 1.8}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/cameras/"+cameraID+"/lenses", map[string]any{"lensIds": []string{lensID}}), http.StatusOK)
	harness.expect(harness.call("PUT", "/cameras/"+cameraID+"/lenses", map[string]any{"lensIds": []string{}}), http.StatusOK)

	// Link rows have no owner column; they take the account of the request.
	links := harness.auditTrail("entityType=camera_lens&entityId=" + cameraID)
	if len(links) != 2 || links[0]["action"] != "delete" || links[1]["action"] != "create" || links[0]["actor"] != actorEmail("tester") {
		test.Fatalf("link audit = %v", links)
	}

	harness.expect(harness.call("PUT", "/film-stocks/"+newID(), map[string]any{
		"brand": "Kodak", "name": "Gold", "type": "color", "boxIso": 200, "process": "C-41", "packaging": "factory",
	}), http.StatusCreated)
	if stocks := harness.auditTrail("entityType=film_stock"); len(stocks) != 1 || stocks[0]["action"] != "create" {
		test.Fatalf("film stock audit = %v", stocks)
	}
}
