package integration_test

import (
	"bytes"
	"net/http"
	"testing"
)

func TestFilmStocksBaseStocksAndInventory(test *testing.T) {
	harness := newHarness(test)

	saveStock := func(stockID string, overrides map[string]any) response {
		body := map[string]any{
			"brand": "Kodak", "name": "Vision3 250D", "type": "color", "boxIso": "250",
			"process": "ECN-2", "packaging": "factory",
		}
		for key, value := range overrides {
			body[key] = value
		}
		return harness.call("PUT", "/film-stocks/"+stockID, body)
	}
	baseID, cyberpunkID, reflxID := newID(), newID(), newID()
	harness.expect(saveStock(baseID, nil), http.StatusCreated)
	harness.expect(saveStock(cyberpunkID, map[string]any{"brand": "Cyberpunk", "name": "400D", "baseStockId": baseID, "packaging": "repack"}), http.StatusCreated)
	harness.expect(saveStock(reflxID, map[string]any{"brand": "Reflx Lab", "name": "320D", "baseStockId": baseID, "packaging": "repack"}), http.StatusCreated)

	// UC-12: self reference, cycle, unknown base.
	harness.expect(saveStock(baseID, map[string]any{"baseStockId": baseID}), http.StatusUnprocessableEntity)
	harness.expect(saveStock(baseID, map[string]any{"baseStockId": cyberpunkID}), http.StatusUnprocessableEntity)
	harness.expect(saveStock(newID(), map[string]any{"baseStockId": newID()}), http.StatusUnprocessableEntity)

	// UC-13.
	repack := harness.expect(harness.call("GET", "/film-stocks/"+cyberpunkID, nil), http.StatusOK)
	if siblings := asList(repack.Body["siblings"]); len(siblings) != 1 {
		test.Fatalf("siblings = %s", repack.Raw)
	}
	base := harness.expect(harness.call("GET", "/film-stocks/"+baseID, nil), http.StatusOK)
	if derived := asList(base.Body["derived"]); len(derived) != 2 {
		test.Fatalf("derived = %s", base.Raw)
	}

	// UC-10 warnings and validation.
	mismatch := harness.expect(saveStock(newID(), map[string]any{"name": "HP5", "type": "bw", "process": "C-41"}), http.StatusCreated)
	if warnings := asList(mismatch.Body["warnings"]); len(warnings) != 1 {
		test.Fatalf("warnings = %s", mismatch.Raw)
	}
	harness.expect(saveStock(newID(), map[string]any{"brand": " "}), http.StatusUnprocessableEntity)
	harness.expect(saveStock(newID(), map[string]any{"type": "nope"}), http.StatusBadRequest)

	// UC-14.
	addRoll := func(stockID string, format int, status string, expiryYear, expiryMonth any) {
		harness.execSQL(`INSERT INTO roll (id, film_stock_id, format, exposures, status, expiry_year, expiry_month, owner_id)
		                 SELECT $1, $2, $3, 36, $4, $5, $6, owner_id FROM film_stock WHERE id = $2`, newID(), stockID, format, status, expiryYear, expiryMonth)
	}
	addRoll(cyberpunkID, 135, "in_stock", 2027, 6)
	addRoll(cyberpunkID, 135, "in_stock", 2026, nil) // unknown month sorts as December 2026
	addRoll(cyberpunkID, 120, "in_stock", nil, nil)
	addRoll(cyberpunkID, 135, "scanned", 2020, 1) // not in stock
	addRoll(reflxID, 135, "in_stock", 2028, 1)

	inventory := harness.listOf("/inventory")
	if len(inventory) != 2 || asObject(inventory[0]["stock"])["brand"] != "Cyberpunk" {
		test.Fatalf("inventory = %v", inventory)
	}
	soonest := asObject(inventory[0]["soonestExpiry"])
	if _, hasMonth := soonest["month"]; soonest["year"] != float64(2026) || hasMonth {
		test.Fatalf("soonestExpiry = %v (an unknown month must be omitted)", soonest)
	}
	if formats := asList(inventory[0]["formats"]); len(formats) != 2 {
		test.Fatalf("formats = %v", formats)
	}
	if filtered := harness.listOf("/inventory?iso=250&process=ECN-2&type=color"); len(filtered) != 2 {
		test.Fatalf("filtered inventory = %v", filtered)
	}
	empty := harness.expect(harness.call("GET", "/inventory?iso=800", nil), http.StatusOK)
	if string(bytes.TrimSpace(empty.Raw)) != "[]" {
		test.Fatalf("iso=800 inventory = %s", empty.Raw)
	}
}

func TestFilmStockBoxISOCanBeARange(test *testing.T) {
	harness := newHarness(test)
	body := func(boxISO string) map[string]any {
		return map[string]any{"brand": "Lomography", "name": "Color " + boxISO, "type": "color", "boxIso": boxISO, "process": "C-41", "packaging": "factory"}
	}

	stockID := newID()
	stock := harness.expect(harness.call("PUT", "/film-stocks/"+stockID, body("100-400")), http.StatusCreated)
	if asObject(stock.Body["stock"])["boxIso"] != "100-400" {
		test.Fatalf("stock = %s", stock.Raw)
	}
	harness.expect(harness.call("PUT", "/film-stocks/"+newID(), body("400-100")), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", "/film-stocks/"+newID(), body("fast")), http.StatusBadRequest)

	// The inventory filter finds a range by any speed inside it, and not by one outside it.
	harness.expect(harness.postBulk("", map[string]any{"filmStockId": stockID, "format": 135, "quantity": 1}), http.StatusCreated)
	if items := harness.listOf("/inventory?iso=200"); len(items) != 1 {
		test.Fatalf("items = %v", items)
	}
	if items := harness.listOf("/inventory?iso=800"); len(items) != 0 {
		test.Fatalf("items = %v", items)
	}
}
