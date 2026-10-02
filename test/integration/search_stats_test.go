package integration_test

import (
	"bytes"
	"net/http"
	"testing"
)

func TestSearchAndStatistics(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)

	// One roll shot on the 40mm fixed lens with scans, one on the adapted 50mm.
	fortyMmRollID := harness.finishedRoll(seeded)
	labID, jobID := newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/rolls/"+fortyMmRollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop_scan", "price": 90000}), http.StatusCreated)
	harness.expect(harness.uploadScans(jobID, "noritsu", "", []upload{
		{fileName: "000001.jpg", content: jpegBytes}, {fileName: "000002.jpg", content: jpegBytes},
	}), http.StatusOK)

	fiftyMmRollID := harness.addRolls(seeded, 1)[0]
	harness.expect(harness.call("PUT", "/rolls/"+fiftyMmRollID+"/load", map[string]any{
		"cameraId": seeded.slrCamera, "lensIds": []string{seeded.fiftyMmLensID},
	}), http.StatusOK)

	// UC-35.
	fortyMm := harness.listOf("/search/rolls?focalLength=40")
	if len(fortyMm) != 1 || asObject(fortyMm[0]["roll"])["id"] != fortyMmRollID || len(asList(fortyMm[0]["scans"])) != 2 {
		test.Fatalf("40mm search = %v", fortyMm)
	}
	fiftyMm := harness.expect(harness.call("GET", "/search/rolls?focalLength=50", nil), http.StatusOK)
	if !bytes.Contains(fiftyMm.Raw, []byte(fiftyMmRollID)) {
		test.Fatalf("50mm search = %s", fiftyMm.Raw)
	}
	if none := harness.listOf("/search/rolls?focalLength=85"); len(none) != 0 {
		test.Fatalf("85mm search = %v", none)
	}

	// UC-36 to UC-39.
	gearStats := harness.expect(harness.call("GET", "/stats/gear", nil), http.StatusOK)
	if len(asList(gearStats.Body["cameras"])) != 2 || len(asList(gearStats.Body["lenses"])) != 2 {
		test.Fatalf("gear = %s", gearStats.Raw)
	}
	if films := harness.listOf("/stats/film"); len(films) != 1 || films[0]["rolls"] != float64(2) {
		test.Fatalf("film = %v", films)
	}
	if byBase := harness.listOf("/stats/film?groupByBase=true"); len(byBase) != 1 {
		test.Fatalf("film by base = %v", byBase)
	}
	if timeline := harness.listOf("/stats/timeline"); len(timeline) != 1 || len(asList(timeline[0]["months"])) != 12 {
		test.Fatalf("timeline = %v", timeline)
	}
	if spending := harness.listOf("/stats/spending"); len(spending) != 1 || spending[0]["processingCost"] != float64(90000) || spending[0]["incomplete"] != true {
		test.Fatalf("spending = %v", spending)
	}
}
