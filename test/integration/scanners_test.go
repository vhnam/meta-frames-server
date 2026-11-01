package integration_test

import (
	"net/http"
	"testing"
)

const (
	noritsuID  = "d177cb3c-a850-4232-a4ca-75189b7b279b"
	frontierID = "e2bf4cbd-87a8-4a17-8340-516b1a1cc6bc"
)

func TestScannersAreBuiltInOrOwnedByOneAccount(test *testing.T) {
	harness := newHarness(test)

	builtIn := harness.listOf("/scanners")
	if len(builtIn) != 2 || builtIn[0]["name"] != "Frontier SP-3000" || builtIn[1]["id"] != noritsuID || builtIn[1]["builtIn"] != true {
		test.Fatalf("built-in scanners = %v", builtIn)
	}
	harness.expect(harness.call("PUT", "/scanners/"+noritsuID, map[string]any{"brand": "Noritsu", "model": "HS-1800"}), http.StatusConflict)
	harness.expect(harness.call("DELETE", "/scanners/"+frontierID, nil), http.StatusConflict)
	harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "NORITSU", "model": "hs-1800"}), http.StatusConflict)

	rig := harness.expect(harness.call("POST", "/scanners", map[string]any{
		"brand": "Sony", "model": "A7R IV + Laowa 100mm", "kind": "camera", "notes": "CS-Lite, Negative Lab Pro",
	}), http.StatusCreated)
	rigID := rig.Body["id"].(string)
	if rig.Body["name"] != "Sony A7R IV + Laowa 100mm" || rig.Body["builtIn"] != false || rig.Body["kind"] != "camera" {
		test.Fatalf("rig = %s", rig.Raw)
	}
	harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "sony", "model": "a7r iv + laowa 100mm"}), http.StatusConflict)
	harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": " ", "model": "A1"}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "Sony", "model": "A1", "kind": "phone"}), http.StatusBadRequest)
	if scanners := harness.listOf("/scanners"); len(scanners) != 3 || scanners[2]["id"] != rigID {
		test.Fatalf("scanners = %v", scanners)
	}

	// Another account sees the catalog only, and cannot use or touch the rig.
	if scanners := harness.listAs("bob", "/scanners"); len(scanners) != 2 {
		test.Fatalf("bob's scanners = %v", scanners)
	}
	harness.expect(harness.callAs("bob", "GET", "/scanners/"+rigID, nil), http.StatusNotFound)
	harness.expect(harness.callAs("bob", "PUT", "/scanners/"+rigID, map[string]any{"brand": "Sony", "model": "A1"}), http.StatusConflict)
	harness.expect(harness.callAs("bob", "DELETE", "/scanners/"+rigID, nil), http.StatusNotFound)

	// A job orders the rig by id; its scans carry the rig's id and name.
	seeded := seedGear(harness)
	rollID, jobID := harness.finishedRoll(seeded), newID()
	sent := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{
		"type": "develop_scan", "scanOrders": []map[string]any{{"scannerId": rigID, "settings": map[string]any{"pixel_shift": true, "stitch_shots": 4}}, {"scannerId": noritsuID}},
	}), http.StatusCreated)
	orders := asList(sent.Body["scanOrders"])
	if first := asObject(orders[0]); len(orders) != 2 || first["scannerName"] != "Noritsu HS-1800" || first["scanner"] != "noritsu" {
		test.Fatalf("orders = %v", orders)
	}
	if second := asObject(orders[1]); second["scannerId"] != rigID || second["scanner"] != "other" || asObject(second["settings"])["stitch_shots"] != float64(4) {
		test.Fatalf("orders = %v", orders)
	}
	imported := harness.expect(harness.uploadScans(jobID, rigID, "", []upload{{fileName: "000003.jpg", content: jpegBytes}}), http.StatusOK)
	scan := asObject(asList(imported.Body["imported"])[0])
	if scan["scannerId"] != rigID || scan["scannerName"] != "Sony A7R IV + Laowa 100mm" {
		test.Fatalf("scan = %v", scan)
	}
	harness.expect(harness.uploadScans(jobID, frontierID, "", []upload{{fileName: "000003.jpg", content: jpegBytes}}), http.StatusUnprocessableEntity)
	if grid := harness.listOf("/processing/" + jobID + "/scans?scannerId=" + rigID); len(grid) != 1 {
		test.Fatalf("grid = %v", grid)
	}
	compared := harness.expect(harness.call("GET", "/processing/"+jobID+"/frames/3/compare", nil), http.StatusOK)
	if scans := asList(compared.Body["scans"]); len(scans) != 1 || len(asList(compared.Body["missing"])) != 2 {
		test.Fatalf("compare = %s", compared.Raw)
	}
	frames := harness.listOf("/rolls/" + rollID + "/frames")
	if refs := asList(frames[0]["scans"]); asObject(refs[0])["scannerName"] != "Sony A7R IV + Laowa 100mm" {
		test.Fatalf("frames = %v", frames)
	}

	// A scanner in use cannot be deleted, an unused one can.
	harness.expect(harness.call("DELETE", "/scanners/"+rigID, nil), http.StatusConflict)
	spare := harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "Epson", "model": "V850"}), http.StatusCreated)
	harness.expect(harness.call("DELETE", "/scanners/"+spare.Body["id"].(string), nil), http.StatusNoContent)
	harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "Epson", "model": "V850"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{
		"type": "develop_scan", "scanOrders": []map[string]any{{"scannerId": newID()}},
	}), http.StatusUnprocessableEntity)
}

func TestOldScannerCodesStillWork(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID, jobID := harness.finishedRoll(seeded), newID()

	// "other" becomes the account's own "Unknown scanner", created once.
	body := map[string]any{"type": "develop_scan", "scanOrders": []map[string]any{{"scanner": "other"}, {"scanner": "frontier"}}}
	sent := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, body), http.StatusCreated)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, body), http.StatusOK)
	unknown := asObject(asList(sent.Body["scanOrders"])[1])
	if unknown["scanner"] != "other" || unknown["scannerName"] != "Unknown scanner" {
		test.Fatalf("orders = %s", sent.Raw)
	}
	if scanners := harness.listOf("/scanners"); len(scanners) != 3 || scanners[2]["id"] != unknown["scannerId"] {
		test.Fatalf("scanners = %v", scanners)
	}
	harness.expect(harness.uploadScans(jobID, "other", "", []upload{{fileName: "000001.jpg", content: jpegBytes}}), http.StatusOK)
	if grid := harness.listOf("/processing/" + jobID + "/scans?scanner=other"); len(grid) != 1 || grid[0]["scannerId"] != unknown["scannerId"] {
		test.Fatalf("grid = %v", grid)
	}

	// Renaming it to the real machine keeps the scans attached.
	harness.expect(harness.call("PUT", "/scanners/"+unknown["scannerId"].(string), map[string]any{"brand": "Epson", "model": "V850", "kind": "flatbed"}), http.StatusOK)
	if grid := harness.listOf("/processing/" + jobID + "/scans"); len(grid) != 1 || grid[0]["scannerName"] != "Epson V850" {
		test.Fatalf("grid after rename = %v", grid)
	}
}

func TestScanSettingsFollowTheScannersKind(test *testing.T) {
	harness := newHarness(test)

	kinds := harness.listOf("/scanner-kinds")
	if len(kinds) != 7 || kinds[0]["kind"] != "minilab" || kinds[6]["kind"] != nil || len(asList(kinds[5]["options"])) != 4 {
		test.Fatalf("kinds = %v", kinds)
	}

	harness.expect(harness.call("POST", "/scanners", map[string]any{
		"brand": "Epson", "model": "V850", "kind": "flatbed", "defaultSettings": map[string]any{"pixel_shift": true},
	}), http.StatusUnprocessableEntity)
	epson := harness.expect(harness.call("POST", "/scanners", map[string]any{
		"brand": "Epson", "model": "V850", "kind": "flatbed", "defaultSettings": map[string]any{"dpi": 3200, "dust_removal": true},
	}), http.StatusCreated)
	epsonID := epson.Body["id"].(string)
	if asObject(epson.Body["defaultSettings"])["dpi"] != float64(3200) {
		test.Fatalf("epson = %s", epson.Raw)
	}

	seeded := seedGear(harness)
	rollID, jobID := harness.finishedRoll(seeded), newID()
	path := "/rolls/" + rollID + "/processing/" + jobID
	sent := harness.expect(harness.call("PUT", path, map[string]any{
		"type": "develop_scan", "scanOrders": []map[string]any{{"scannerId": epsonID}, {"scannerId": noritsuID, "hiRes": true}},
	}), http.StatusCreated)
	orders := asList(sent.Body["scanOrders"])
	epsonOrder, noritsuOrder := asObject(orders[0]), asObject(orders[1])
	if asObject(epsonOrder["settings"])["dust_removal"] != true || epsonOrder["hiRes"] != false {
		test.Fatalf("epson order = %v", epsonOrder)
	}
	if asObject(noritsuOrder["settings"])["resolution"] != "hi_res" || noritsuOrder["hiRes"] != true {
		test.Fatalf("noritsu order = %v", noritsuOrder)
	}
	for _, settings := range []map[string]any{{"resolution": "hi_res"}, {"dpi": 99999}, {"dpi": "3200"}} {
		harness.expect(harness.call("PUT", path, map[string]any{
			"type": "develop_scan", "scanOrders": []map[string]any{{"scannerId": epsonID, "settings": settings}},
		}), http.StatusUnprocessableEntity)
	}
	harness.expect(harness.call("PUT", path, map[string]any{
		"type": "develop_scan", "scanOrders": []map[string]any{{"scannerId": epsonID, "settings": map[string]any{"dpi": 6400, "format": "tiff"}}},
	}), http.StatusOK)
	jobs := harness.listOf("/rolls/" + rollID + "/processing")
	settings := asObject(asObject(asList(jobs[0]["scanOrders"])[0])["settings"])
	if settings["dpi"] != float64(6400) || settings["format"] != "tiff" || settings["dust_removal"] != nil {
		test.Fatalf("settings = %v", settings)
	}
}
