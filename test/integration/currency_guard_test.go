package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// send sends a raw body with the given header lines as the default signed-in account.
func (harness *harness) send(method, path, body string, headers http.Header) response {
	harness.test.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	request.Header.Set("Content-Type", "application/json")
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	return harness.serve(request)
}

func asJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func expected(values ...string) http.Header { return http.Header{"Expected-Currency": values} }

func (harness *harness) testerID() string {
	harness.test.Helper()
	var userID string
	if err := harness.pool.QueryRow(context.Background(), "SELECT id FROM app_user WHERE email = $1", actorEmail("tester")).Scan(&userID); err != nil {
		harness.test.Fatal(err)
	}
	return userID
}

func (harness *harness) setTesterCurrency(code string, locked bool) {
	harness.test.Helper()
	harness.execSQL("UPDATE app_user SET currency_code = $1, currency_locked = $2 WHERE email = $3", code, locked, actorEmail("tester"))
}

// holdTx runs statement in an open transaction and returns its commit, so a test can hold a lock
// while requests queue behind it.
func (harness *harness) holdTx(statement string, arguments ...any) (release func()) {
	harness.test.Helper()
	ctx := context.Background()
	transaction, err := harness.pool.Begin(ctx)
	if err != nil {
		harness.test.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, statement, arguments...); err != nil {
		harness.test.Fatal(err)
	}
	released := false
	harness.test.Cleanup(func() {
		if !released {
			_ = transaction.Rollback(ctx)
		}
	})
	return func() {
		released = true
		if err := transaction.Commit(ctx); err != nil {
			harness.test.Fatal(err)
		}
	}
}

func (harness *harness) holdAccount() func() {
	return harness.holdTx("SELECT 1 FROM app_user WHERE email = $1 FOR NO KEY UPDATE", actorEmail("tester"))
}

func (harness *harness) holdKey(key string) func() {
	return harness.holdTx("SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2::text, 0))", harness.testerID(), key)
}

// waitForWaiters blocks until count sessions wait on a lock.
func (harness *harness) waitForWaiters(count int) {
	harness.test.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if harness.count("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'") >= count {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	harness.test.Fatalf("fewer than %d sessions are waiting on a lock", count)
}

// later starts a request in the background.
func (harness *harness) later(request func() response) <-chan response {
	result := make(chan response, 1)
	go func() { result <- request() }()
	return result
}

func (harness *harness) await(result <-chan response) response {
	harness.test.Helper()
	select {
	case reply := <-result:
		return reply
	case <-time.After(20 * time.Second):
		harness.test.Fatal("a request did not finish: deadlock?")
		return response{}
	}
}

func (harness *harness) testerState() currencyState { return harness.storedCurrency("tester") }

func rollBody(seeded gear, price any) string {
	body := map[string]any{"filmStockId": seeded.stockID, "format": 135, "exposures": 36}
	if price != nil {
		body["price"] = price
	}
	return asJSON(body)
}

func bulkBody(seeded gear, quantity int, price any) string {
	body := map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": quantity}
	if price != nil {
		body["price"] = price
	}
	return asJSON(body)
}

func idempotent(key string, extra http.Header) http.Header {
	headers := http.Header{"Idempotency-Key": {key}}
	for name, values := range extra {
		headers[name] = values
	}
	return headers
}

func (harness *harness) saveCurrency(code, expectedCode string) response {
	return harness.send("PUT", "/auth/me/currency", asJSON(map[string]string{"currencyCode": code, "expectedCurrencyCode": expectedCode}), nil)
}

func TestFirstPricedWriteAndSettingSaveTakeTurns(test *testing.T) {
	for _, order := range []string{"update first", "save first"} {
		test.Run(order, func(test *testing.T) {
			harness := newHarness(test)
			seeded := seedGear(harness)
			rollID := harness.addRolls(seeded, 1)[0]
			release := harness.holdAccount()
			update := func() response { return harness.send("PUT", "/rolls/"+rollID, rollBody(seeded, 1250), nil) }
			save := func() response { return harness.saveCurrency("USD", "VND") }

			var updated, saved <-chan response
			if order == "update first" {
				updated = harness.later(update)
				harness.waitForWaiters(1)
				saved = harness.later(save)
			} else {
				saved = harness.later(save)
				harness.waitForWaiters(1)
				updated = harness.later(update)
			}
			harness.waitForWaiters(2)
			release()
			updateResult, saveResult := harness.await(updated), harness.await(saved)

			if order == "update first" {
				if updateResult.Status != http.StatusOK || saveResult.Status != http.StatusConflict || saveResult.Body["code"] != "currency_locked" {
					test.Fatalf("update %d %s, save %d %s", updateResult.Status, updateResult.Raw, saveResult.Status, saveResult.Raw)
				}
				if got := harness.testerState(); got != (currencyState{"VND", true}) {
					test.Fatalf("state = %+v", got)
				}
				return
			}
			if saveResult.Status != http.StatusOK || updateResult.Status != http.StatusConflict || updateResult.Body["code"] != "currency_mismatch" {
				test.Fatalf("save %d %s, update %d %s", saveResult.Status, saveResult.Raw, updateResult.Status, updateResult.Raw)
			}
			if got := harness.testerState(); got != (currencyState{"USD", false}) {
				test.Fatalf("state = %+v", got)
			}
			if harness.count("SELECT count(*) FROM roll WHERE price IS NOT NULL") != 0 {
				test.Fatal("the refused update wrote a price")
			}
		})
	}
}

// A price-free edit inserts an audit row while a priced update holds the account lock and
// waits for the roll. The audit foreign key must let the notes transaction finish.
func TestPricedUpdateAndSettingSaveDoNotDeadlockOnAHeldRoll(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	notes, err := harness.pool.Begin(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = notes.Rollback(context.Background()) }()
	if _, err := notes.Exec(ctx, "SELECT 1 FROM roll WHERE id = $1 FOR UPDATE", rollID); err != nil {
		test.Fatal(err)
	}

	updated := harness.later(func() response { return harness.send("PUT", "/rolls/"+rollID, rollBody(seeded, 1250), nil) })
	harness.waitForWaiters(1)
	saved := harness.later(func() response { return harness.saveCurrency("USD", "VND") })
	harness.waitForWaiters(2)
	if _, err := notes.Exec(ctx, "UPDATE roll SET description = 'Price-free notes' WHERE id = $1", rollID); err != nil {
		test.Fatalf("price-free notes and audit write: %v", err)
	}
	if err := notes.Commit(ctx); err != nil {
		test.Fatal(err)
	}
	if result := harness.await(updated); result.Status != http.StatusOK {
		test.Fatalf("update %d %s", result.Status, result.Raw)
	}
	if result := harness.await(saved); result.Status != http.StatusConflict || result.Body["code"] != "currency_locked" {
		test.Fatalf("save %d %s", result.Status, result.Raw)
	}
}

func TestFailureAfterTheGuardLeavesNothingBehind(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	harness.execSQL(`CREATE FUNCTION fail_priced_roll() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.price = 777 THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$`)
	harness.execSQL("CREATE TRIGGER fail_priced_roll BEFORE INSERT ON roll FOR EACH ROW EXECUTE FUNCTION fail_priced_roll()")

	rolls := harness.count("SELECT count(*) FROM roll")
	audits := harness.count("SELECT count(*) FROM audit_log")
	result := harness.send("POST", "/rolls/bulk", bulkBody(seeded, 2, 777), idempotent("failing", nil))
	if result.Status != http.StatusInternalServerError {
		test.Fatalf("status = %d %s, want the injected failure", result.Status, result.Raw)
	}
	if got := harness.testerState(); got != (currencyState{"VND", false}) {
		test.Fatalf("the guard's lock survived the failure: %+v", got)
	}
	if harness.count("SELECT count(*) FROM roll") != rolls || harness.count("SELECT count(*) FROM audit_log") != audits ||
		harness.count("SELECT count(*) FROM idempotency_key") != 0 {
		test.Fatal("the failed request left rolls, audit rows or a key behind")
	}
}

func TestBulkReplayReturnsTheFirstResponse(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)

	first := harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 2, 1250), idempotent("key-1", nil)), http.StatusCreated)
	var firstRolls []map[string]any
	_ = json.Unmarshal(first.Raw, &firstRolls)
	if len(firstRolls) != 2 {
		test.Fatalf("first = %s", first.Raw)
	}
	// A lost response: the same request again gives the same rolls, not more.
	harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 2, 1250), idempotent("key-1", nil)), http.StatusCreated)
	if harness.count("SELECT count(*) FROM roll") != 2 {
		test.Fatal("the retry created rolls")
	}

	// The rolls change, and the retry still returns the first response, whatever its header says.
	harness.execSQL("UPDATE roll SET price = 1500 WHERE id = $1", firstRolls[0]["id"])
	harness.execSQL("UPDATE roll SET deleted_at = now() WHERE id = $1", firstRolls[1]["id"])
	for _, header := range [][]string{{"usd"}, {""}, {"VND", "USD"}} {
		retry := harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 2, 1250), idempotent("key-1", expected(header...))), http.StatusCreated)
		var retried []map[string]any
		_ = json.Unmarshal(retry.Raw, &retried)
		if !reflect.DeepEqual(retried, firstRolls) {
			test.Fatalf("retry with header %q = %s\nwant %s", header, retry.Raw, first.Raw)
		}
	}
	if harness.count("SELECT count(*) FROM roll") != 2 {
		test.Fatal("a replay created rolls")
	}
}

func TestBulkReplayRebuildsALegacyKeyFromTheCurrentRows(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollIDs := harness.addRolls(seeded, 2)
	harness.execSQL("UPDATE roll SET price = 1500 WHERE id = $1", rollIDs[0])
	harness.execSQL(`INSERT INTO idempotency_key (key, owner_id, status, response) VALUES ('legacy', $1, 201, $2::jsonb)`,
		harness.testerID(), asJSON(rollIDs))

	replay := harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 5, 1), idempotent("legacy", expected("usd"))), http.StatusCreated)
	var rolls []map[string]any
	_ = json.Unmarshal(replay.Raw, &rolls)
	if len(rolls) != 2 || rolls[0]["id"] != rollIDs[0] || rolls[0]["price"] != float64(1500) || rolls[1]["id"] != rollIDs[1] {
		test.Fatalf("legacy replay = %s", replay.Raw)
	}
	if harness.count("SELECT count(*) FROM roll") != 2 {
		test.Fatal("a replay created rolls")
	}
}

func TestSameKeyRequestsTakeTurns(test *testing.T) {
	test.Run("price-free winner, priced loser", func(test *testing.T) {
		harness := newHarness(test)
		seeded := seedGear(harness)
		release := harness.holdKey("shared")
		winner := harness.later(func() response {
			return harness.send("POST", "/rolls/bulk", bulkBody(seeded, 2, nil), idempotent("shared", nil))
		})
		harness.waitForWaiters(1)
		loser := harness.later(func() response {
			return harness.send("POST", "/rolls/bulk", bulkBody(seeded, 2, 1250), idempotent("shared", expected("USD")))
		})
		harness.waitForWaiters(2)
		release()
		winning, losing := harness.await(winner), harness.await(loser)
		if winning.Status != http.StatusCreated || losing.Status != http.StatusCreated || !bytes.Equal(winning.Raw, losing.Raw) {
			test.Fatalf("winner %d %s\nloser %d %s", winning.Status, winning.Raw, losing.Status, losing.Raw)
		}
		if got := harness.testerState(); got != (currencyState{"VND", false}) {
			test.Fatalf("the loser changed the account: %+v", got)
		}
		if harness.count("SELECT count(*) FROM roll") != 2 {
			test.Fatal("the loser created rolls")
		}
	})

	for _, order := range []string{"handoff first", "bulk first"} {
		test.Run(order, func(test *testing.T) {
			harness := newHarness(test)
			seeded := seedGear(harness)
			labID := harness.newLab("Lab A")
			rollID := harness.finishedRoll(seeded)
			handoff := func() response {
				return harness.send("POST", "/lab-handoffs", asJSON(handOver(labID, handed(rollID, "develop"))), idempotent("shared", nil))
			}
			// The handoff-first case sends a priced bulk with a wrong header: it must not read it.
			// The bulk-first case records a price and locks the account before the handoff replays it.
			var bulk func() response
			if order == "handoff first" {
				bulk = func() response {
					return harness.send("POST", "/rolls/bulk", bulkBody(seeded, 3, 1250), idempotent("shared", expected("USD")))
				}
			} else {
				bulk = func() response {
					return harness.send("POST", "/rolls/bulk", bulkBody(seeded, 3, 1250), idempotent("shared", nil))
				}
			}
			release := harness.holdKey("shared")
			var firstResult, secondResult <-chan response
			if order == "handoff first" {
				firstResult = harness.later(handoff)
				harness.waitForWaiters(1)
				secondResult = harness.later(bulk)
			} else {
				firstResult = harness.later(bulk)
				harness.waitForWaiters(1)
				secondResult = harness.later(handoff)
			}
			harness.waitForWaiters(2)
			release()
			first, second := harness.await(firstResult), harness.await(secondResult)
			if first.Status != http.StatusCreated || second.Status != http.StatusCreated {
				test.Fatalf("first %d %s\nsecond %d %s", first.Status, first.Raw, second.Status, second.Raw)
			}
			var firstRolls, secondRolls []map[string]any
			_ = json.Unmarshal(first.Raw, &firstRolls)
			_ = json.Unmarshal(second.Raw, &secondRolls)
			if len(firstRolls) == 0 || len(secondRolls) != len(firstRolls) || secondRolls[0]["id"] != firstRolls[0]["id"] {
				test.Fatalf("the second request did not replay the first:\nfirst %s\nsecond %s", first.Raw, second.Raw)
			}
			if got := harness.testerState(); got != (currencyState{"VND", order == "bulk first"}) {
				test.Fatalf("account = %+v", got)
			}
			if order == "handoff first" && harness.count("SELECT count(*) FROM roll") != 1 {
				test.Fatal("the bulk request created rolls after the handoff")
			}
			if order == "bulk first" && harness.count("SELECT count(*) FROM lab_handoff") != 0 {
				test.Fatal("the handoff ran after the bulk request")
			}
		})
	}
}

func TestClaimOfAKeyAfterTheGuardRollsTheGuardBack(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	// A key row from outside the advisory lock: invisible to the lookup, but the claim must wait for it.
	release := harness.holdTx("INSERT INTO idempotency_key (key, owner_id) VALUES ('raced', $1)", harness.testerID())
	result := harness.later(func() response {
		return harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 1250), idempotent("raced", nil))
	})
	harness.waitForWaiters(1)
	release()
	reply := harness.await(result)
	if reply.Status != http.StatusConflict || reply.Body["code"] != "request_in_progress" {
		test.Fatalf("status = %d %s", reply.Status, reply.Raw)
	}
	if got := harness.testerState(); got != (currencyState{"VND", false}) {
		test.Fatalf("the guard's lock survived: %+v", got)
	}
	if harness.count("SELECT count(*) FROM roll") != 0 {
		test.Fatal("rolls were written")
	}
}

func TestMismatchLeavesNoKeyAndARetryWithTheRightCodeWorks(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	harness.setTesterCurrency("USD", false)
	refused := harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 1250), idempotent("fresh", expected("VND")))
	if refused.Status != http.StatusConflict || refused.Body["code"] != "currency_mismatch" {
		test.Fatalf("refused = %d %s", refused.Status, refused.Raw)
	}
	if harness.count("SELECT count(*) FROM idempotency_key") != 0 || harness.count("SELECT count(*) FROM roll") != 0 {
		test.Fatal("a refused request left a key or rolls")
	}
	harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 1250), idempotent("fresh", expected("USD"))), http.StatusCreated)
	if got := harness.testerState(); got != (currencyState{"USD", true}) {
		test.Fatalf("state = %+v", got)
	}
}

func TestBulkWithoutAKey(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	harness.setTesterCurrency("USD", false)
	// Priced: both steps run.
	if refused := harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 1250), nil); refused.Status != http.StatusConflict {
		test.Fatalf("priced, no header, USD account: %d %s", refused.Status, refused.Raw)
	}
	if refused := harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 1250), expected("usd")); refused.Status != http.StatusBadRequest {
		test.Fatalf("priced, bad header: %d %s", refused.Status, refused.Raw)
	}
	// Price-free: neither runs, so even an invalid header succeeds and nothing locks.
	harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, nil), expected("usd")), http.StatusCreated)
	harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, nil), expected("VND", "EUR")), http.StatusCreated)
	if got := harness.testerState(); got != (currencyState{"USD", false}) {
		test.Fatalf("state = %+v", got)
	}
}

func TestExpectedCurrencyHeaderMatrix(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	writes := map[string]func(header http.Header) response{
		"POST /rolls/bulk": func(header http.Header) response {
			return harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 1250), header)
		},
		"PUT /rolls/{id}": func(header http.Header) response {
			return harness.send("PUT", "/rolls/"+rollID, rollBody(seeded, 1250), header)
		},
	}
	other := map[string]string{"VND": "EUR", "USD": "VND", "EUR": "USD"}
	for name, write := range writes {
		for _, account := range []string{"VND", "USD"} {
			for _, locked := range []bool{false, true} {
				cases := []struct {
					header http.Header
					want   int
				}{
					{expected(account), 0}, // 0: success, whatever the success status is
					{nil, map[bool]int{true: 0, false: http.StatusConflict}[account == "VND"]},
					{expected(other[account]), http.StatusConflict},
					{expected(""), http.StatusBadRequest},
					{expected(account, account), http.StatusBadRequest},
					{expected("usd"), http.StatusBadRequest},
				}
				for index, testCase := range cases {
					harness.setTesterCurrency(account, locked)
					result := write(testCase.header)
					ok := result.Status == http.StatusOK || result.Status == http.StatusCreated
					if (testCase.want == 0) != ok || (testCase.want != 0 && result.Status != testCase.want) {
						test.Errorf("%s, %s account locked=%v, case %d (%v): status = %d %s", name, account, locked, index, testCase.header, result.Status, result.Raw)
						continue
					}
					wantLocked := locked || ok
					if got := harness.testerState(); got != (currencyState{account, wantLocked}) {
						test.Errorf("%s, %s account locked=%v, case %d: state after = %+v, want locked=%v", name, account, locked, index, got, wantLocked)
					}
				}
			}
		}
	}
}

func TestStaleDraftAfterAnotherSessionLocked(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	harness.expect(harness.saveCurrency("EUR", "VND"), http.StatusOK)
	// Another session records money in EUR and locks the account.
	harness.expect(harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 900), expected("EUR")), http.StatusCreated)
	rolls := harness.count("SELECT count(*) FROM roll")
	stale := harness.send("POST", "/rolls/bulk", bulkBody(seeded, 1, 900), expected("VND"))
	if stale.Status != http.StatusConflict || stale.Body["code"] != "currency_mismatch" {
		test.Fatalf("stale draft = %d %s", stale.Status, stale.Raw)
	}
	if harness.count("SELECT count(*) FROM roll") != rolls {
		test.Fatal("the stale draft wrote a roll")
	}
}

func TestPriceFreeRollUpdateIgnoresTheHeaderButKeepsIfMatch(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	harness.setTesterCurrency("USD", false)
	for _, header := range []http.Header{expected("usd"), expected(""), expected("VND", "USD"), expected("EUR")} {
		harness.expect(harness.send("PUT", "/rolls/"+rollID, rollBody(seeded, nil), header), http.StatusOK)
	}
	if got := harness.testerState(); got != (currencyState{"USD", false}) {
		test.Fatalf("a price-free update changed the account: %+v", got)
	}
	stale := http.Header{"If-Match": {`"99"`}}
	harness.expect(harness.send("PUT", "/rolls/"+rollID, rollBody(seeded, nil), stale), http.StatusPreconditionFailed)
}

func TestMoneyRoutesRefuseDataAfterTheBody(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	labID := harness.newLab("Lab A")
	finished := harness.finishedRoll(seeded)
	orderBody := asJSON(orderBody(labID, "0510006", newID(), newID()))
	processingBody := asJSON(map[string]any{"labId": labID, "type": "develop", "price": 900})

	routes := []struct{ method, path, body string }{
		{"POST", "/rolls/bulk", bulkBody(seeded, 1, 900)},
		{"PUT", "/rolls/" + rollID, rollBody(seeded, 900)},
		{"PUT", "/rolls/" + finished + "/processing/" + newID(), processingBody},
		{"PUT", "/lab-orders/" + newID(), orderBody},
	}
	for _, route := range routes {
		rolls, jobs, orders := harness.count("SELECT count(*) FROM roll"), harness.count("SELECT count(*) FROM processing"), harness.count("SELECT count(*) FROM lab_order")
		if result := harness.send(route.method, route.path, route.body+"{}", nil); result.Status != http.StatusBadRequest {
			test.Errorf("%s %s with trailing data: status = %d %s", route.method, route.path, result.Status, result.Raw)
		}
		if harness.count("SELECT count(*) FROM roll") != rolls || harness.count("SELECT count(*) FROM processing") != jobs ||
			harness.count("SELECT count(*) FROM lab_order") != orders {
			test.Errorf("%s %s wrote on a doubtful body", route.method, route.path)
		}
		if result := harness.send(route.method, route.path, route.body, nil); result.Status != http.StatusCreated && result.Status != http.StatusOK {
			test.Errorf("%s %s with a clean body: status = %d %s", route.method, route.path, result.Status, result.Raw)
		}
	}
	if got := harness.testerState(); got.code != "VND" {
		test.Fatalf("state = %+v", got)
	}
}

func TestPricedRollNotesEditChecksCurrency(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	harness.setTesterCurrency("USD", false)
	harness.expect(harness.send("PUT", "/rolls/"+rollID, rollBody(seeded, 1250), expected("USD")), http.StatusOK)

	body := asJSON(map[string]any{
		"filmStockId": seeded.stockID, "format": 135, "exposures": 36, "price": 1250, "description": "Notes only",
	})
	path := "/rolls/" + rollID
	refused := harness.expect(harness.send("PUT", path, body, nil), http.StatusConflict)
	if refused.Body["code"] != "currency_mismatch" {
		test.Fatalf("refused notes edit = %s", refused.Raw)
	}
	detail := harness.expect(harness.call("GET", path, nil), http.StatusOK)
	if asObject(detail.Body["roll"])["description"] != nil {
		test.Fatalf("refused edit changed the notes: %s", detail.Raw)
	}
	updated := harness.expect(harness.send("PUT", path, body, expected("USD")), http.StatusOK)
	roll := asObject(updated.Body["roll"])
	if roll["description"] != "Notes only" || roll["price"] != float64(1250) {
		test.Fatalf("notes edit = %s", updated.Raw)
	}
}
