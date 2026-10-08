package integration_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCamerasAndLensesFollowTheGearRules(test *testing.T) {
	harness := newHarness(test)

	// UC-07: a fixed-lens camera creates camera + built-in lens + link together.
	fixedCameraID := newID()
	fixedCamera := map[string]any{
		"brand": "Canon", "model": "Canonet QL17", "hasFixedLens": true,
		"fixedLens": map[string]any{"focalLength": 40, "maxAperture": 1.7},
	}
	created := harness.expect(harness.call("PUT", "/cameras/"+fixedCameraID, fixedCamera), http.StatusCreated)
	builtInLensID, _ := created.Body["builtInLensId"].(string)
	if builtInLensID == "" {
		test.Fatalf("expected builtInLensId: %s", created.Raw)
	}
	harness.expect(harness.call("PUT", "/cameras/"+fixedCameraID, fixedCamera), http.StatusOK) // retry is idempotent

	linked := harness.listOf("/cameras/" + fixedCameraID + "/lenses")
	if len(linked) != 1 || linked[0]["isBuiltIn"] != true || linked[0]["maxAperture"] != 1.7 {
		test.Fatalf("linked lenses = %v", linked)
	}

	// UC-01 validation and UC-02 immutability of the fixed-lens flag.
	harness.expect(harness.call("PUT", "/cameras/"+newID(), map[string]any{"brand": "Nikon", "model": "FM2", "hasFixedLens": false}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/cameras/"+newID(), map[string]any{
		"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": true,
		"fixedLens": map[string]any{"focalLength": 50, "maxAperture": 2},
	}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/cameras/"+newID(), map[string]any{"brand": "Nikon", "model": "FM2", "hasFixedLens": true}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/cameras/"+fixedCameraID, map[string]any{"brand": "Canon", "model": "QL17", "mount": "x", "hasFixedLens": false}), http.StatusConflict)

	// UC-04 and UC-08.
	interchangeableID, lensFiftyID, lensTwentyEightID := newID(), newID(), newID()
	harness.expect(harness.call("PUT", "/cameras/"+interchangeableID, map[string]any{"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": false}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lenses/"+lensFiftyID, map[string]any{"brand": "Nikon", "model": "50 f/1.4", "mount": "F", "focalLength": 50, "maxAperture": 1.4}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lenses/"+lensTwentyEightID, map[string]any{"brand": "Nikon", "model": "28 f/2.8", "mount": "F", "focalLength": 28, "maxAperture": 2.8}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lenses/"+newID(), map[string]any{"brand": "x", "model": "y", "mount": "F", "focalLength": 50, "maxAperture": 1.45}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/lenses/"+newID(), map[string]any{"focalLength": 50, "maxAperture": 1.4}), http.StatusUnprocessableEntity)

	harness.expect(harness.call("PUT", "/cameras/"+fixedCameraID+"/lenses", map[string]any{"lensIds": []string{lensFiftyID}}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/cameras/"+interchangeableID+"/lenses", map[string]any{"lensIds": []string{builtInLensID}}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/cameras/"+interchangeableID+"/lenses", map[string]any{"lensIds": []string{newID()}}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/cameras/"+interchangeableID+"/lenses", map[string]any{"lensIds": []string{lensFiftyID, lensTwentyEightID, lensFiftyID}}), http.StatusOK)
	replaced := harness.expect(harness.call("PUT", "/cameras/"+interchangeableID+"/lenses", map[string]any{"lensIds": []string{lensTwentyEightID}}), http.StatusOK)
	var remaining []map[string]any
	_ = json.Unmarshal(replaced.Raw, &remaining)
	if len(remaining) != 1 || remaining[0]["id"] != lensTwentyEightID {
		test.Fatalf("lenses after replace = %s", replaced.Raw)
	}

	// UC-06: a built-in lens follows its camera.
	harness.expect(harness.call("PUT", "/lenses/"+builtInLensID+"/active", map[string]any{"isActive": false}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/lenses/"+lensTwentyEightID+"/active", map[string]any{"isActive": false}), http.StatusOK)

	// UC-03 and UC-09: a loaded camera cannot be deactivated and shows its roll.
	stockID, rollID := newID(), newID()
	harness.expect(harness.call("PUT", "/film-stocks/"+stockID, map[string]any{
		"brand": "Kodak", "name": "UltraMax 400", "type": "color", "boxIso": 400, "process": "C-41", "packaging": "factory",
	}), http.StatusCreated)
	harness.execSQL(`INSERT INTO roll (id, film_stock_id, camera_id, format, exposures, status, started_at, owner_id)
	                 SELECT $1, $2, $3, 135, 36, 'in_camera', CURRENT_DATE - 3, owner_id FROM film_stock WHERE id = $2`, rollID, stockID, fixedCameraID)
	harness.expect(harness.call("PUT", "/cameras/"+fixedCameraID+"/active", map[string]any{"isActive": false}), http.StatusConflict)

	camera := harness.expect(harness.call("GET", "/cameras/"+fixedCameraID, nil), http.StatusOK)
	loaded, _ := camera.Body["loadedRoll"].(map[string]any)
	if loaded == nil || loaded["stockName"] != "UltraMax 400" || loaded["shotIso"] != float64(400) || loaded["daysLoaded"] != float64(3) {
		test.Fatalf("loadedRoll = %v", camera.Body["loadedRoll"])
	}

	harness.execSQL(`UPDATE roll SET status = 'done_shooting' WHERE id = $1`, rollID)
	deactivated := harness.expect(harness.call("PUT", "/cameras/"+fixedCameraID+"/active", map[string]any{"isActive": false}), http.StatusOK)
	if deactivated.Body["isActive"] != false {
		test.Fatal("camera should be inactive")
	}
	builtInLens := harness.expect(harness.call("GET", "/lenses/"+builtInLensID, nil), http.StatusOK)
	if builtInLens.Body["isActive"] != false {
		test.Fatal("built-in lens should follow the camera")
	}
	if active := harness.listOf("/cameras?activeOnly=true"); len(active) != 1 {
		test.Fatalf("active cameras = %v", active)
	}
}
