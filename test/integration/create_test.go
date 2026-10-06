package integration_test

import (
	"net/http"
	"testing"
)

func TestPostCreatesEntitiesWithServerAssignedIDs(test *testing.T) {
	harness := newHarness(test)

	camera := harness.expect(harness.call("POST", "/cameras", map[string]any{"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": false}), http.StatusCreated)
	lens := harness.expect(harness.call("POST", "/lenses", map[string]any{"brand": "Nikon", "model": "50", "mount": "F", "focalLength": 50, "maxAperture": 1.8}), http.StatusCreated)
	stock := harness.expect(harness.call("POST", "/film-stocks", map[string]any{
		"brand": "Kodak", "name": "Gold", "type": "color", "boxIso": 200, "process": "C-41", "packaging": "factory",
	}), http.StatusCreated)
	lab := harness.expect(harness.call("POST", "/labs", map[string]any{"name": "Lab A"}), http.StatusCreated)

	cameraID, _ := camera.Body["id"].(string)
	lensID, _ := lens.Body["id"].(string)
	stockID, _ := asObject(stock.Body["stock"])["id"].(string)
	labID, _ := lab.Body["id"].(string)
	for resource, id := range map[string]string{"/cameras/": cameraID, "/lenses/": lensID, "/film-stocks/": stockID, "/labs/": labID} {
		if id == "" {
			test.Fatalf("%s: the response must carry the new id", resource)
		}
		harness.expect(harness.call("GET", resource+id, nil), http.StatusOK)
	}

	// Two POSTs create two records: unlike PUT, POST is not idempotent.
	harness.expect(harness.call("POST", "/labs", map[string]any{"name": "Lab A"}), http.StatusCreated)
	if labs := harness.listOf("/labs"); len(labs) != 2 {
		test.Fatalf("labs = %v", labs)
	}

	// Validation still applies.
	harness.expect(harness.call("POST", "/cameras", map[string]any{"brand": "Nikon", "model": "FM2", "hasFixedLens": false}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("POST", "/labs", map[string]any{"name": "  "}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("POST", "/lenses", map[string]any{"focalLength": 50, "maxAperture": 1.8}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("POST", "/film-stocks", map[string]any{"brand": "Kodak"}), http.StatusBadRequest)
}
