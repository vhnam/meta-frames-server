package integration_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// listAs GETs a path that returns a JSON array, signed in as the named user.
func (harness *harness) listAs(name, path string) []any {
	harness.test.Helper()
	result := harness.expect(harness.callAs(name, "GET", path, nil), http.StatusOK)
	var items []any
	if err := json.Unmarshal(result.Raw, &items); err != nil {
		harness.test.Fatalf("GET %s: %s", path, result.Raw)
	}
	return items
}

func TestAccountsOnlySeeTheirOwnRecords(test *testing.T) {
	harness := newHarness(test)

	// "tester" (harness.call) builds a full history: gear, a shot roll, a lab job and scans.
	seeded := seedGear(harness)
	rollID := harness.finishedRoll(seeded)
	harness.addRolls(seeded, 2)
	labID, jobID := newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{
		"labId": labID, "type": "develop_scan", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	imported := harness.expect(harness.uploadScans(jobID, "noritsu", "", []upload{{fileName: "000001.jpg", content: jpegBytes}}), http.StatusOK)
	scanID := asObject(asList(imported.Body["imported"])[0])["id"].(string)
	auditID := harness.listOf("/audit-logs")[0]["id"]

	// Another account sees none of it.
	for _, path := range []string{
		"/cameras", "/lenses", "/film-stocks", "/labs", "/rolls", "/inventory", "/negatives-at-lab", "/audit-logs",
		"/search/rolls?focalLength=40", "/stats/film", "/stats/timeline",
	} {
		if items := harness.listAs("bob", path); len(items) != 0 {
			test.Fatalf("bob sees %d items at %s: %v", len(items), path, items)
		}
	}
	gear := harness.expect(harness.callAs("bob", "GET", "/stats/gear", nil), http.StatusOK)
	if len(asList(gear.Body["cameras"])) != 0 || len(asList(gear.Body["lenses"])) != 0 {
		test.Fatalf("bob's gear stats = %v", gear.Body)
	}
	expiry := harness.expect(harness.callAs("bob", "GET", "/expiry", nil), http.StatusOK)
	if string(expiry.Raw) == "" {
		test.Fatal("expiry must answer")
	}

	for _, path := range []string{
		"/cameras/" + seeded.slrCamera, "/lenses/" + seeded.fiftyMmLensID, "/film-stocks/" + seeded.stockID, "/labs/" + labID,
		"/rolls/" + rollID, "/processing/" + jobID, "/scans/" + scanID, "/scans/" + scanID + "/file",
		"/rolls/" + rollID + "/frames", "/processing/" + jobID + "/scans", "/cameras/" + seeded.slrCamera + "/lenses",
	} {
		harness.expect(harness.callAs("bob", "GET", path, nil), http.StatusNotFound)
	}
	harness.expect(harness.callAs("bob", "GET", "/audit-logs/"+jsonNumber(auditID), nil), http.StatusNotFound)

	// Bob cannot change, delete or point at tester's records either.
	harness.expect(harness.callAs("bob", "PUT", "/cameras/"+seeded.slrCamera, map[string]any{
		"brand": "Stolen", "model": "FM2", "mount": "F", "hasFixedLens": false,
	}), http.StatusConflict)
	harness.expect(harness.callAs("bob", "DELETE", "/cameras/"+seeded.slrCamera, nil), http.StatusNotFound)
	harness.expect(harness.callAs("bob", "DELETE", "/rolls/"+rollID, nil), http.StatusNotFound)
	harness.expect(harness.callAs("bob", "DELETE", "/scans/"+scanID, nil), http.StatusNotFound)
	if status := harness.callAs("bob", "POST", "/rolls/bulk", map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 1}).Status; status < 400 || status >= 500 {
		test.Fatalf("bob added rolls of tester's stock: %d", status)
	}
	bobStock := newID()
	harness.expect(harness.callAs("bob", "PUT", "/film-stocks/"+bobStock, map[string]any{
		"brand": "Ilford", "name": "HP5", "type": "bw", "boxIso": 400, "process": "BW", "packaging": "factory",
	}), http.StatusCreated)
	bobRoll := harness.expect(harness.callAs("bob", "POST", "/rolls/bulk", map[string]any{"filmStockId": bobStock, "format": 135, "quantity": 1}), http.StatusCreated)
	var bobRolls []map[string]any
	_ = json.Unmarshal(bobRoll.Raw, &bobRolls)
	if status := harness.callAs("bob", "PUT", "/rolls/"+bobRolls[0]["id"].(string)+"/load", map[string]any{"cameraId": seeded.slrCamera}).Status; status < 400 || status >= 500 {
		test.Fatalf("bob loaded a roll into tester's camera: %d", status)
	}

	// Tester's records are untouched and still all visible to tester.
	camera := harness.expect(harness.call("GET", "/cameras/"+seeded.slrCamera, nil), http.StatusOK)
	if camera.Body["brand"] != "Nikon" {
		test.Fatalf("tester's camera changed: %v", camera.Body)
	}
	if rolls := harness.listOf("/rolls"); len(rolls) != 3 {
		test.Fatalf("tester's rolls = %d, want 3", len(rolls))
	}
	if items := harness.listAs("bob", "/rolls"); len(items) != 1 {
		test.Fatalf("bob's rolls = %d, want 1", len(items))
	}
	for _, entry := range harness.listAs("bob", "/audit-logs") {
		if asObject(entry)["actor"] != actorEmail("bob") {
			test.Fatalf("bob sees someone else's audit entry: %v", entry)
		}
	}
}

func TestRecordsFromBeforeAccountsGoToTheNextAccount(test *testing.T) {
	harness := newHarness(test)
	orphan := newID()
	harness.execSQL("INSERT INTO camera (id, brand, model, mount) VALUES ($1, 'Leica', 'M3', 'M')", orphan)

	harness.expect(harness.callAs("carol", "GET", "/cameras/"+orphan, nil), http.StatusOK) // carol signs up here
	harness.expect(harness.call("GET", "/cameras/"+orphan, nil), http.StatusNotFound)
	// Carol inherits the camera's history, and handing it over is not logged as a change.
	if entries := harness.listAs("carol", "/audit-logs?entityType=camera"); len(entries) != 1 || asObject(entries[0])["action"] != "create" {
		test.Fatalf("carol's camera history = %v", entries)
	}
}

func jsonNumber(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
