package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type gear struct {
	stockID       string
	fixedCamera   string
	slrCamera     string
	fiftyMmLensID string
}

// seedGear creates one film stock, a fixed-lens camera (40mm), an SLR and a 50mm lens.
func seedGear(harness *harness) gear {
	harness.test.Helper()
	seeded := gear{stockID: newID(), fixedCamera: newID(), slrCamera: newID(), fiftyMmLensID: newID()}
	harness.expect(harness.call("PUT", "/film-stocks/"+seeded.stockID, map[string]any{
		"brand": "Kodak", "name": "UltraMax 400", "type": "color", "boxIso": 400, "process": "C-41", "packaging": "factory",
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/cameras/"+seeded.fixedCamera, map[string]any{
		"brand": "Canon", "model": "QL17", "hasFixedLens": true,
		"fixedLens": map[string]any{"focalLength": 40, "maxAperture": 1.7},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/cameras/"+seeded.slrCamera, map[string]any{
		"brand": "Nikon", "model": "FM2", "mount": "F", "hasFixedLens": false,
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lenses/"+seeded.fiftyMmLensID, map[string]any{
		"brand": "Nikon", "model": "50", "mount": "F", "focalLength": 50, "maxAperture": 1.4,
	}), http.StatusCreated)
	return seeded
}

// postBulk calls POST /rolls/bulk, optionally with an Idempotency-Key.
func (harness *harness) postBulk(idempotencyKey string, body map[string]any) response {
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", "/rolls/bulk", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return harness.serve(request)
}

// addRolls bulk-adds rolls of the seeded stock and returns their ids.
func (harness *harness) addRolls(seeded gear, quantity int) []string {
	harness.test.Helper()
	result := harness.expect(harness.postBulk("", map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": quantity}), http.StatusCreated)
	var rolls []map[string]any
	_ = json.Unmarshal(result.Raw, &rolls)
	rollIDs := make([]string, len(rolls))
	for index, roll := range rolls {
		rollIDs[index] = roll["id"].(string)
	}
	return rollIDs
}

// finishedRoll returns a roll that was loaded into the fixed-lens camera and finished.
func (harness *harness) finishedRoll(seeded gear) string {
	harness.test.Helper()
	rollID := harness.addRolls(seeded, 1)[0]
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/load", map[string]any{"cameraId": seeded.fixedCamera}), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/finish", nil), http.StatusOK)
	return rollID
}

func TestBulkAddingRollsIsRetrySafeAndValidated(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)

	bulk := map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 3, "price": 150000, "expiry": map[string]any{"year": 2027, "month": 6}}
	first := harness.postBulk("key-1", bulk)
	retry := harness.postBulk("key-1", bulk)
	if first.Status != http.StatusCreated || !bytes.Equal(first.Raw, retry.Raw) {
		test.Fatalf("retry differs: %d %s vs %s", first.Status, first.Raw, retry.Raw)
	}
	var rolls []map[string]any
	_ = json.Unmarshal(first.Raw, &rolls)
	if len(rolls) != 3 || rolls[0]["exposures"] != float64(36) || rolls[0]["status"] != "in_stock" {
		test.Fatalf("bulk = %s", first.Raw)
	}
	if inStock := harness.listOf("/rolls?status=in_stock"); len(inStock) != 3 {
		test.Fatalf("in-stock rolls = %d, want 3 (a retry must not duplicate)", len(inStock))
	}

	cases := []struct {
		name       string
		body       map[string]any
		wantStatus int
	}{
		{"120 film needs exposures", map[string]any{"filmStockId": seeded.stockID, "format": 120, "quantity": 1}, http.StatusUnprocessableEntity},
		{"month needs a year", map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 1, "expiry": map[string]any{"month": 3}}, http.StatusBadRequest},
		{"quantity must be positive", map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 0}, http.StatusBadRequest},
		{"unknown stock", map[string]any{"filmStockId": newID(), "format": 135, "quantity": 1}, http.StatusUnprocessableEntity},
	}
	for _, testCase := range cases {
		if result := harness.postBulk("", testCase.body); result.Status != testCase.wantStatus {
			test.Errorf("%s: status = %d, want %d", testCase.name, result.Status, testCase.wantStatus)
		}
	}
}

func TestLoadingFinishingAndExpiringRolls(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)

	harness.postBulk("", map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 1, "expiry": map[string]any{"year": 2020}}) // expired
	harness.postBulk("", map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 1})                                         // no expiry
	freshRolls := harness.addRolls(seeded, 2)

	// UC-21.
	report := harness.expect(harness.call("GET", "/expiry", nil), http.StatusOK)
	expiring := asList(report.Body["expiring"])
	if len(expiring) != 1 || asObject(expiring[0])["expired"] != true || len(asList(report.Body["noExpiry"])) != 3 {
		test.Fatalf("expiry = %s", report.Raw)
	}
	expiredRollID := asObject(asObject(expiring[0])["roll"])["id"].(string)

	// UC-16.
	edited := harness.expect(harness.call("PUT", "/rolls/"+freshRolls[1], map[string]any{
		"filmStockId": seeded.stockID, "format": 135, "exposures": 24, "price": 160000,
		"expiry": map[string]any{"year": 2028, "month": 9},
	}), http.StatusOK)
	if expiry := asObject(asObject(edited.Body["roll"])["expiry"]); expiry["year"] != float64(2028) || expiry["month"] != float64(9) {
		test.Fatalf("expiry = %v", edited.Body["roll"])
	}

	// UC-17: a fixed-lens camera assigns its own lens; a second roll is blocked.
	loaded := harness.expect(harness.call("PUT", "/rolls/"+freshRolls[0]+"/load", map[string]any{"cameraId": seeded.fixedCamera, "shotIso": 400}), http.StatusOK)
	summary := asObject(loaded.Body["roll"])
	if summary["status"] != "in_camera" {
		test.Fatalf("load = %s", loaded.Raw)
	}
	if _, hasShotISO := summary["shotIso"]; hasShotISO {
		test.Fatal("a shot ISO equal to the box ISO must be stored as null")
	}
	if lenses := asList(loaded.Body["lenses"]); len(lenses) != 1 || asObject(lenses[0])["isBuiltIn"] != true {
		test.Fatalf("lenses = %v", loaded.Body["lenses"])
	}
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[0]+"/load", map[string]any{"cameraId": seeded.fixedCamera}), http.StatusOK) // same load again
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[1]+"/load", map[string]any{"cameraId": seeded.fixedCamera}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[0]+"/lenses", map[string]any{"lensIds": []string{seeded.fiftyMmLensID}}), http.StatusConflict)

	// An expired roll loads with a warning; a pushed ISO is kept; an adapted lens is allowed.
	pushed := harness.expect(harness.call("PUT", "/rolls/"+expiredRollID+"/load", map[string]any{
		"cameraId": seeded.slrCamera, "shotIso": 800, "lensIds": []string{seeded.fiftyMmLensID},
	}), http.StatusOK)
	if warnings := asList(pushed.Body["warnings"]); len(warnings) != 1 {
		test.Fatalf("expired warning = %v", pushed.Body["warnings"])
	}
	if asObject(pushed.Body["roll"])["shotIso"] != float64(800) {
		test.Fatalf("shotIso = %s", pushed.Raw)
	}
	harness.expect(harness.call("PUT", "/cameras/"+seeded.slrCamera+"/active", map[string]any{"isActive": false}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/rolls/"+expiredRollID+"/lenses", map[string]any{"lensIds": []string{}}), http.StatusOK)

	// UC-19.
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[1]+"/finish", nil), http.StatusConflict) // still in stock
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[0]+"/finish", map[string]any{"date": "2026-09-01"}), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[0]+"/finish", nil), http.StatusConflict)
	harness.expect(harness.call("PUT", "/rolls/"+freshRolls[1]+"/load", map[string]any{"cameraId": seeded.fixedCamera}), http.StatusOK) // camera is free again
}
