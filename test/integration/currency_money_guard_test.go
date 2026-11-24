package integration_test

import (
	"maps"
	"net/http"
	"testing"
)

// moneyWrite is one write the guard covers: a request, and a count of the rows it writes.
type moneyWrite struct {
	send  func(header http.Header) response
	count func() int
}

func isSuccess(result response) bool {
	return result.Status == http.StatusOK || result.Status == http.StatusCreated
}

func orderPayload(labID, code string, lines map[string]int, vat, discount int) string {
	items := []map[string]any{}
	for lineID, unitPrice := range lines {
		items = append(items, map[string]any{"id": lineID, "product": "film " + lineID[:4], "quantity": 3, "unitPrice": unitPrice})
	}
	return asJSON(map[string]any{
		"labId": labID, "code": code, "orderedAt": "2026-10-05T10:03:00+07:00", "vat": vat, "discount": discount, "items": items,
	})
}

// moneyWrites builds the three writes of this slice on an account that has no money yet: a job
// saved with its own price, a lab order create, and a lab order update. The account is left unlocked.
func moneyWrites(harness *harness) map[string]moneyWrite {
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	finished := harness.finishedRoll(seeded)
	jobID := newID()
	harness.expect(harness.send("PUT", "/rolls/"+finished+"/processing/"+jobID, asJSON(map[string]any{"labId": labID, "type": "develop"}), nil), http.StatusCreated)
	orderID, lineOne, lineTwo := newID(), newID(), newID()
	lines := map[string]int{lineOne: 125000, lineTwo: 145000}
	harness.expect(harness.send("PUT", "/lab-orders/"+orderID, orderPayload(labID, "fixed", lines, 100, 0), nil), http.StatusCreated)
	harness.setTesterCurrency("VND", false)

	return map[string]moneyWrite{
		"processing price": {
			send: func(header http.Header) response {
				return harness.send("PUT", "/rolls/"+finished+"/processing/"+jobID, asJSON(map[string]any{"labId": labID, "type": "develop", "price": 900}), header)
			},
			count: func() int { return harness.count("SELECT count(*) FROM processing WHERE price IS NOT NULL") },
		},
		"lab order create": {
			send: func(header http.Header) response {
				return harness.send("PUT", "/lab-orders/"+newID(), orderPayload(labID, newID(), map[string]int{newID(): 1000}, 0, 0), header)
			},
			count: func() int { return harness.count("SELECT count(*) FROM lab_order") },
		},
		"lab order update": {
			send: func(header http.Header) response {
				return harness.send("PUT", "/lab-orders/"+orderID, orderPayload(labID, "fixed", lines, 200, 0), header)
			},
			count: func() int { return harness.count("SELECT count(*) FROM lab_order WHERE vat = 200") },
		},
	}
}

func TestFirstProcessingOrOrderWriteAndSettingSaveTakeTurns(test *testing.T) {
	for _, name := range []string{"processing price", "lab order create", "lab order update"} {
		for _, order := range []string{"write first", "save first"} {
			test.Run(name+", "+order, func(test *testing.T) {
				harness := newHarness(test)
				write := moneyWrites(harness)[name]
				release := harness.holdAccount()
				doWrite := func() response { return write.send(nil) }
				doSave := func() response { return harness.saveCurrency("USD", "VND") }

				var written, saved <-chan response
				if order == "write first" {
					written = harness.later(doWrite)
					harness.waitForWaiters(1)
					saved = harness.later(doSave)
				} else {
					saved = harness.later(doSave)
					harness.waitForWaiters(1)
					written = harness.later(doWrite)
				}
				harness.waitForWaiters(2)
				release()
				writeResult, saveResult := harness.await(written), harness.await(saved)

				if order == "write first" {
					if !isSuccess(writeResult) || saveResult.Status != http.StatusConflict || saveResult.Body["code"] != "currency_locked" {
						test.Fatalf("write %d %s, save %d %s", writeResult.Status, writeResult.Raw, saveResult.Status, saveResult.Raw)
					}
					if got := harness.testerState(); got != (currencyState{"VND", true}) {
						test.Fatalf("state = %+v", got)
					}
					return
				}
				if saveResult.Status != http.StatusOK || writeResult.Status != http.StatusConflict || writeResult.Body["code"] != "currency_mismatch" {
					test.Fatalf("save %d %s, write %d %s", saveResult.Status, saveResult.Raw, writeResult.Status, writeResult.Raw)
				}
				if got := harness.testerState(); got != (currencyState{"USD", false}) {
					test.Fatalf("state = %+v", got)
				}
			})
		}
	}
}

func TestFailureAfterTheGuardLeavesNothingBehindForJobsAndOrders(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	finished := harness.finishedRoll(seeded)
	jobID := newID()
	harness.expect(harness.send("PUT", "/rolls/"+finished+"/processing/"+jobID, asJSON(map[string]any{"labId": labID, "type": "develop"}), nil), http.StatusCreated)
	newJobRoll := harness.finishedRoll(seeded)
	orderID, lineID := newID(), newID()
	harness.expect(harness.send("PUT", "/lab-orders/"+orderID, orderPayload(labID, "existing", map[string]int{lineID: 500}, 0, 0), nil), http.StatusCreated)
	harness.expect(harness.send("PUT", "/processing/"+jobID+"/order-items", asJSON(map[string]any{"orderItemIds": []string{lineID}}), nil), http.StatusOK)
	harness.setTesterCurrency("VND", false)

	harness.execSQL(`CREATE FUNCTION fail_priced_job() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.price = 777 THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$`)
	harness.execSQL(`CREATE FUNCTION fail_priced_line() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.unit_price = 777 THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$`)
	harness.execSQL("CREATE TRIGGER fail_priced_job BEFORE INSERT OR UPDATE ON processing FOR EACH ROW EXECUTE FUNCTION fail_priced_job()")
	harness.execSQL("CREATE TRIGGER fail_priced_line BEFORE INSERT ON lab_order_item FOR EACH ROW EXECUTE FUNCTION fail_priced_line()")

	before := harness.fingerprints()
	jobBody := asJSON(map[string]any{"labId": labID, "type": "develop", "price": 777})
	failures := []struct{ name, path, body string }{
		{"job create", "/rolls/" + newJobRoll + "/processing/" + newID(), jobBody},
		{"job update", "/rolls/" + finished + "/processing/" + jobID, jobBody},
		{"order create", "/lab-orders/" + newID(), orderPayload(labID, "x", map[string]int{newID(): 777}, 5, 0)},
		{"order update", "/lab-orders/" + orderID, orderPayload(labID, "existing", map[string]int{lineID: 777}, 5, 0)},
	}
	for _, failure := range failures {
		result := harness.send("PUT", failure.path, failure.body, nil)
		if result.Status != http.StatusInternalServerError {
			test.Errorf("%s: status = %d %s, want the injected failure", failure.name, result.Status, result.Raw)
		}
		if got := harness.testerState(); got != (currencyState{"VND", false}) {
			test.Errorf("%s: the guard's lock survived: %+v", failure.name, got)
		}
		if after := harness.fingerprints(); !maps.Equal(before, after) {
			test.Fatalf("%s: stored rows or derived costs changed: before %v, after %v", failure.name, before, after)
		}
	}
}

func TestMixedProcessingPriceFailsWithoutChangingCurrencyOrRows(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID, lineID := harness.newLab("Lab A"), newID()
	harness.expect(harness.send("PUT", "/lab-orders/"+newID(), orderPayload(labID, "mixed-price", map[string]int{lineID: 500}, 0, 0), nil), http.StatusCreated)
	harness.setTesterCurrency("USD", false)
	path := "/rolls/" + harness.finishedRoll(seeded) + "/processing/" + newID()
	body := asJSON(map[string]any{"labId": labID, "type": "develop", "price": 900, "orderItemIds": []string{lineID}})
	before := harness.fingerprints()
	for _, check := range []struct {
		currency string
		status   int
		code     string
	}{
		{"VND", http.StatusConflict, "currency_mismatch"},
		{"USD", http.StatusUnprocessableEntity, "price_from_order"},
	} {
		result := harness.expect(harness.send("PUT", path, body, expected(check.currency)), check.status)
		if result.Body["code"] != check.code {
			test.Fatalf("header %s: response = %s, want %s", check.currency, result.Raw, check.code)
		}
		if got := harness.testerState(); got != (currencyState{"USD", false}) || !maps.Equal(before, harness.fingerprints()) {
			test.Fatalf("header %s: rejected write changed the account or rows: %+v", check.currency, got)
		}
	}
}

func TestStaleDraftAfterAnotherSessionLockedForJobsAndOrders(test *testing.T) {
	for _, name := range []string{"processing price", "lab order create", "lab order update"} {
		test.Run(name, func(test *testing.T) {
			harness := newHarness(test)
			write := moneyWrites(harness)[name]
			harness.expect(harness.saveCurrency("EUR", "VND"), http.StatusOK)
			if result := write.send(expected("EUR")); !isSuccess(result) {
				test.Fatalf("write in EUR: %d %s", result.Status, result.Raw)
			}
			rows := write.count()
			stale := write.send(expected("VND"))
			if stale.Status != http.StatusConflict || stale.Body["code"] != "currency_mismatch" {
				test.Fatalf("stale draft = %d %s", stale.Status, stale.Raw)
			}
			if name == "lab order update" {
				// The refused update must not change the order the first write already changed.
				if write.count() != rows {
					test.Fatal("the stale draft changed rows")
				}
				return
			}
			if write.count() != rows {
				test.Fatal("the stale draft wrote rows")
			}
		})
	}
}

func TestExpectedCurrencyHeaderMatrixForJobsAndOrders(test *testing.T) {
	harness := newHarness(test)
	writes := moneyWrites(harness)
	other := map[string]string{"VND": "EUR", "USD": "VND", "EUR": "USD"}
	for name, write := range writes {
		for _, account := range []string{"VND", "USD"} {
			for _, locked := range []bool{false, true} {
				cases := []struct {
					header http.Header
					want   int // 0: success
				}{
					{expected(account), 0},
					{nil, map[bool]int{true: 0, false: http.StatusConflict}[account == "VND"]},
					{expected(other[account]), http.StatusConflict},
					{expected(""), http.StatusBadRequest},
					{expected(account, account), http.StatusBadRequest},
					{expected("usd"), http.StatusBadRequest},
				}
				for index, testCase := range cases {
					harness.setTesterCurrency(account, locked)
					result := write.send(testCase.header)
					ok := isSuccess(result)
					if (testCase.want == 0) != ok || (testCase.want != 0 && result.Status != testCase.want) {
						test.Errorf("%s, %s account locked=%v, case %d (%v): status = %d %s", name, account, locked, index, testCase.header, result.Status, result.Raw)
						continue
					}
					if got := harness.testerState(); got != (currencyState{account, locked || ok}) {
						test.Errorf("%s, %s account locked=%v, case %d: state after = %+v", name, account, locked, index, got)
					}
				}
			}
		}
	}
}

func TestLabOrderWithZeroTotalsIsGuarded(test *testing.T) {
	harness := newHarness(test)
	labID := harness.newLab("Lab A")
	orderID, lineID := newID(), newID()
	zero := orderPayload(labID, "zero", map[string]int{lineID: 0}, 0, 0)

	if refused := harness.send("PUT", "/lab-orders/"+orderID, zero, expected("USD")); refused.Status != http.StatusConflict {
		test.Fatalf("zero order in the wrong currency: %d %s", refused.Status, refused.Raw)
	}
	harness.expect(harness.send("PUT", "/lab-orders/"+orderID, zero, nil), http.StatusCreated)
	if got := harness.testerState(); got != (currencyState{"VND", true}) {
		test.Fatalf("a zero-total create must lock: %+v", got)
	}
	harness.setTesterCurrency("VND", false)
	harness.expect(harness.send("PUT", "/lab-orders/"+orderID, zero, nil), http.StatusOK)
	if got := harness.testerState(); got != (currencyState{"VND", true}) {
		test.Fatalf("a zero-total update must lock: %+v", got)
	}
}

func TestWritesWithoutAStandalonePriceIgnoreTheHeader(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	lineID := newID()
	harness.expect(harness.send("PUT", "/lab-orders/"+newID(), orderPayload(labID, "L", map[string]int{lineID: 50000}, 0, 0), nil), http.StatusCreated)
	harness.setTesterCurrency("VND", false)
	bad := expected("usd")
	unlocked := func(step string) {
		test.Helper()
		if got := harness.testerState(); got != (currencyState{"VND", false}) {
			test.Fatalf("%s changed the account: %+v", step, got)
		}
	}

	nilPrice, byLines, linkLater := harness.finishedRoll(seeded), harness.finishedRoll(seeded), harness.finishedRoll(seeded)
	jobNil, jobByLines, jobLinkLater := newID(), newID(), newID()
	harness.expect(harness.send("PUT", "/rolls/"+nilPrice+"/processing/"+jobNil, asJSON(map[string]any{"labId": labID, "type": "develop"}), bad), http.StatusCreated)
	unlocked("a job with no price")
	harness.expect(harness.send("PUT", "/rolls/"+byLines+"/processing/"+jobByLines, asJSON(map[string]any{"labId": labID, "type": "develop", "orderItemIds": []string{lineID}}), bad), http.StatusCreated)
	unlocked("a job priced by its order lines")
	harness.expect(harness.send("PUT", "/rolls/"+linkLater+"/processing/"+jobLinkLater, asJSON(map[string]any{"labId": labID, "type": "develop"}), nil), http.StatusCreated)
	harness.expect(harness.send("PUT", "/processing/"+jobLinkLater+"/order-items", asJSON(map[string]any{"orderItemIds": []string{lineID}}), bad), http.StatusOK)
	unlocked("setProcessingOrderItems")

	handedRoll := harness.finishedRoll(seeded)
	harness.expect(harness.send("POST", "/lab-handoffs", asJSON(handOver(labID, handed(handedRoll, "develop"))), bad), http.StatusCreated)
	unlocked("handoff")
	developedRoll := harness.finishedRoll(seeded)
	harness.expect(harness.send("PUT", "/rolls/"+developedRoll+"/developed", "", bad), http.StatusOK)
	unlocked("mark developed")
}

// expectedUnitCost is the share of VAT and discount a line carries, rounded half away from zero.
func expectedUnitCost(unitPrice, subtotal, vat, discount int) int {
	if subtotal == 0 {
		return 0
	}
	numerator, denominator := unitPrice*subtotal+unitPrice*(vat-discount), subtotal
	if numerator >= 0 {
		return (2*numerator + denominator) / (2 * denominator)
	}
	return -((-2*numerator + denominator) / (2 * denominator))
}

func TestAllocationOfAnOrderIsUnchanged(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")

	type line struct{ price, quantity int }
	cases := []struct {
		name          string
		lines         []line
		vat, discount int
		jobs          [][]int // each job lists the lines it is billed on; a line in no job is unlinked
	}{
		{"VAT only", []line{{125000, 1}, {145000, 3}}, 44800, 0, [][]int{{0, 1}, {1}}},
		{"discount", []line{{125000, 1}, {145000, 3}}, 0, 56000, [][]int{{0, 1}, {1}}},
		{"full discount", []line{{125000, 1}, {145000, 3}}, 0, 560000, [][]int{{0}, {1}}},
		{"zero subtotal", []line{{0, 1}, {0, 2}}, 7, 0, [][]int{{0, 1}}},
		{"half unit tie up", []line{{1, 1}, {1, 1}}, 1, 0, [][]int{{0}, {1}}},
		{"half unit tie down", []line{{1, 1}, {1, 1}}, 0, 1, [][]int{{0, 1}}},
		{"unlinked line", []line{{60000, 1}, {40000, 1}}, 10000, 0, [][]int{{0}}},
	}
	totalCost := 0
	for _, testCase := range cases {
		lineIDs := make([]string, len(testCase.lines))
		items := make([]map[string]any, len(testCase.lines))
		subtotal := 0
		for index, entry := range testCase.lines {
			lineIDs[index] = newID()
			subtotal += entry.price * entry.quantity
			items[index] = map[string]any{"id": lineIDs[index], "product": "p", "quantity": entry.quantity, "unitPrice": entry.price}
		}
		orderID := newID()
		order := harness.expect(harness.send("PUT", "/lab-orders/"+orderID, asJSON(map[string]any{
			"labId": labID, "code": newID(), "orderedAt": "2026-10-05T10:03:00+07:00", "vat": testCase.vat, "discount": testCase.discount, "items": items,
		}), nil), http.StatusCreated)
		wantTotal := subtotal + testCase.vat - testCase.discount
		if order.Body["subtotal"] != float64(subtotal) || order.Body["total"] != float64(wantTotal) {
			test.Errorf("%s: order amounts = %s, want subtotal %d and total %d", testCase.name, order.Raw, subtotal, wantTotal)
		}

		unitCosts := make([]int, len(testCase.lines))
		for index, entry := range testCase.lines {
			unitCosts[index] = expectedUnitCost(entry.price, subtotal, testCase.vat, testCase.discount)
			if got := asObject(asList(order.Body["items"])[index])["unitCost"]; got != float64(unitCosts[index]) {
				test.Errorf("%s: line %d unitCost = %v, want %d", testCase.name, index, got, unitCosts[index])
			}
		}
		for _, lines := range testCase.jobs {
			ids, want := []string{}, 0
			for _, index := range lines {
				ids = append(ids, lineIDs[index])
				want += unitCosts[index]
			}
			rollID := harness.finishedRoll(seeded)
			job := harness.expect(harness.send("PUT", "/rolls/"+rollID+"/processing/"+newID(), asJSON(map[string]any{"labId": labID, "type": "develop", "orderItemIds": ids}), nil), http.StatusCreated)
			if job.Body["price"] != float64(want) {
				test.Errorf("%s: job %v price = %v, want %d", testCase.name, lines, job.Body["price"], want)
			}
			detail := harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK)
			if totals := asObject(detail.Body["totals"]); totals["processingPrice"] != float64(want) {
				test.Errorf("%s: roll totals = %v, want processingPrice %d", testCase.name, totals, want)
			}
			totalCost += want
		}
	}
	// A job with no price yet counts as zero and marks the result incomplete.
	harness.expect(harness.send("PUT", "/rolls/"+harness.finishedRoll(seeded)+"/processing/"+newID(), asJSON(map[string]any{"labId": labID, "type": "develop"}), nil), http.StatusCreated)
	spending := harness.listOf("/stats/spending")
	if len(spending) != 1 || spending[0]["processingCost"] != float64(totalCost) || spending[0]["incomplete"] != true {
		test.Fatalf("spending = %v, want processingCost %d and incomplete", spending, totalCost)
	}
}
