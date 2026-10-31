package integration_test

import (
	"net/http"
	"testing"
)

func TestLabsRunTheirOwnScanners(test *testing.T) {
	harness := newHarness(test)

	// Brand is required, model is not: this lab runs a Noritsu whose model nobody knows.
	noritsu := harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "Noritsu", "kind": "minilab"}), http.StatusCreated)
	noritsuOwnID := noritsu.Body["id"].(string)
	if noritsu.Body["name"] != "Noritsu" || noritsu.Body["model"] != nil {
		test.Fatalf("noritsu = %s", noritsu.Raw)
	}
	harness.expect(harness.call("POST", "/scanners", map[string]any{"brand": "noritsu", "model": " "}), http.StatusConflict)

	labID := newID()
	lab := harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{
		"name": "Lab A", "scannerIds": []string{noritsuOwnID, frontierID, noritsuOwnID},
	}), http.StatusCreated)
	if ids := asList(lab.Body["scannerIds"]); len(ids) != 2 || ids[0] != noritsuOwnID || ids[1] != frontierID {
		test.Fatalf("lab = %s", lab.Raw)
	}

	// Without scannerIds the lab keeps its scanners; the list shows them too.
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab B"}), http.StatusOK)
	if labs := harness.listOf("/labs"); len(labs) != 1 || len(asList(labs[0]["scannerIds"])) != 2 {
		test.Fatalf("labs = %v", labs)
	}

	// Another account cannot give its lab this account's scanner.
	harness.expect(harness.callAs("bob", "PUT", "/labs/"+newID(), map[string]any{
		"name": "Bob's lab", "scannerIds": []string{noritsuOwnID},
	}), http.StatusUnprocessableEntity)

	// A deleted scanner leaves the lab.
	harness.expect(harness.call("DELETE", "/scanners/"+noritsuOwnID, nil), http.StatusNoContent)
	got := harness.expect(harness.call("GET", "/labs/"+labID, nil), http.StatusOK)
	if ids := asList(got.Body["scannerIds"]); len(ids) != 1 || ids[0] != frontierID {
		test.Fatalf("lab = %s", got.Raw)
	}
}
