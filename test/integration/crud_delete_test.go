package integration_test

import (
	"net/http"
	"testing"
)

func TestDeletingCamerasLensesAndFilmStocks(test *testing.T) {
	harness := newHarness(test)

	// Camera: a fixed-lens camera that never held a roll goes away with its built-in lens.
	fixedID := newID()
	created := harness.expect(harness.call("PUT", "/cameras/"+fixedID, map[string]any{
		"brand": "Canon", "model": "QL17", "hasFixedLens": true,
		"fixedLens": map[string]any{"focalLength": 40, "maxAperture": 1.7},
	}), http.StatusCreated)
	builtInID, _ := created.Body["builtInLensId"].(string)
	harness.expect(harness.call("DELETE", "/lenses/"+builtInID, nil), http.StatusConflict)
	harness.expect(harness.call("DELETE", "/cameras/"+fixedID, nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/cameras/"+fixedID, nil), http.StatusNotFound)
	harness.expect(harness.call("GET", "/lenses/"+builtInID, nil), http.StatusNotFound)
	harness.expect(harness.call("DELETE", "/cameras/"+fixedID, nil), http.StatusNotFound)

	// Lens: unused lenses can go, lenses used on a roll cannot.
	freeLensID, usedLensID, cameraID, stockID := newID(), newID(), newID(), newID()
	for _, lensID := range []string{freeLensID, usedLensID} {
		harness.expect(harness.call("PUT", "/lenses/"+lensID, map[string]any{"brand": "Nikon", "model": "50", "mount": "F", "focalLength": 50, "maxAperture": 1.8}), http.StatusCreated)
	}
	harness.expect(harness.call("PUT", "/cameras/"+cameraID, map[string]any{"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": false}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/cameras/"+cameraID+"/lenses", map[string]any{"lensIds": []string{freeLensID}}), http.StatusOK)
	harness.expect(harness.call("DELETE", "/lenses/"+freeLensID, nil), http.StatusNoContent)
	if linked := harness.listOf("/cameras/" + cameraID + "/lenses"); len(linked) != 0 {
		test.Fatalf("deleting a lens must unlink it from cameras, got %v", linked)
	}

	harness.expect(harness.call("PUT", "/film-stocks/"+stockID, map[string]any{
		"brand": "Kodak", "name": "Gold", "type": "color", "boxIso": 200, "process": "C-41", "packaging": "factory",
	}), http.StatusCreated)
	rollIDs := harness.addRolls(gear{stockID: stockID}, 2)

	// A lens used on a roll and a camera that held one are protected; so is a stock with rolls.
	harness.expect(harness.call("PUT", "/cameras/"+cameraID+"/lenses", map[string]any{"lensIds": []string{usedLensID}}), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+rollIDs[0]+"/load", map[string]any{"cameraId": cameraID, "lensIds": []string{usedLensID}}), http.StatusOK)
	harness.expect(harness.call("DELETE", "/lenses/"+usedLensID, nil), http.StatusConflict)
	harness.expect(harness.call("DELETE", "/cameras/"+cameraID, nil), http.StatusConflict)
	harness.expect(harness.call("DELETE", "/film-stocks/"+stockID, nil), http.StatusConflict)

	// Film stock: a base stock cannot go while a derived stock points at it; unused stocks can.
	derivedID := newID()
	harness.expect(harness.call("PUT", "/film-stocks/"+derivedID, map[string]any{
		"brand": "Repack", "name": "Gold 200", "type": "color", "boxIso": 200, "process": "C-41", "packaging": "factory", "baseStockId": stockID,
	}), http.StatusCreated)
	harness.expect(harness.call("DELETE", "/film-stocks/"+derivedID, nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/film-stocks/"+derivedID, nil), http.StatusNotFound)
	harness.expect(harness.call("DELETE", "/film-stocks/"+newID(), nil), http.StatusNotFound)
}

func TestGetLabByID(test *testing.T) {
	harness := newHarness(test)
	labID := newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A", "address": "HCMC"}), http.StatusCreated)

	found := harness.expect(harness.call("GET", "/labs/"+labID, nil), http.StatusOK)
	if found.Body["name"] != "Lab A" || found.Body["address"] != "HCMC" {
		test.Fatalf("lab = %s", found.Raw)
	}
	harness.expect(harness.call("GET", "/labs/"+newID(), nil), http.StatusNotFound)
}
