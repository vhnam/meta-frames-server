package integration_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rawAs sends a body as written, so a test can send malformed JSON.
func (harness *harness) rawAs(name, body string) response {
	harness.test.Helper()
	request := httptest.NewRequest("PUT", "/auth/me/currency", bytes.NewReader([]byte(body)))
	request.Header.Set("Content-Type", "application/json")
	if name == "" {
		// harness.app is anonymous; harness.serve would sign the request in as tester.
		recorder := httptest.NewRecorder()
		harness.app.ServeHTTP(recorder, request)
		return response{Status: recorder.Code, Raw: recorder.Body.Bytes()}
	}
	request.AddCookie(harness.sessionOf(name))
	return harness.serve(request)
}

func (harness *harness) setCurrency(name, code, expected string) response {
	harness.test.Helper()
	return harness.callAs(name, "PUT", "/auth/me/currency", map[string]string{"currencyCode": code, "expectedCurrencyCode": expected})
}

func (harness *harness) lockCurrencyOf(name string) {
	harness.test.Helper()
	harness.execSQL("UPDATE app_user SET currency_locked = true WHERE email = $1", actorEmail(name))
}

func (harness *harness) storedCurrency(name string) currencyState {
	harness.test.Helper()
	var state currencyState
	err := harness.pool.QueryRow(context.Background(),
		"SELECT currency_code, currency_locked FROM app_user WHERE email = $1", actorEmail(name)).Scan(&state.code, &state.locked)
	if err != nil {
		harness.test.Fatal(err)
	}
	return state
}

func TestSetCurrencyRules(test *testing.T) {
	harness := newHarness(test)

	saved := harness.expect(harness.setCurrency("owner", "USD", "VND"), http.StatusOK)
	if saved.Body["currencyCode"] != "USD" || saved.Body["currencyLocked"] != false {
		test.Fatalf("saved = %s", saved.Raw)
	}
	if me := harness.expect(harness.callAs("owner", "GET", "/auth/me", nil), http.StatusOK); me.Body["currencyCode"] != "USD" {
		test.Fatalf("me = %s", me.Raw)
	}

	// A stale expected code is refused and leaves the account as it was.
	if refused := harness.expect(harness.setCurrency("owner", "EUR", "VND"), http.StatusConflict); refused.Body["code"] != "currency_mismatch" {
		test.Fatalf("stale = %s", refused.Raw)
	}
	if got := harness.storedCurrency("owner"); got != (currencyState{"USD", false}) {
		test.Fatalf("after mismatch = %+v", got)
	}

	// The same code is a no-op, also when the account is locked and the expected code is stale.
	harness.expect(harness.setCurrency("owner", "USD", "USD"), http.StatusOK)
	harness.lockCurrencyOf("owner")
	same := harness.expect(harness.setCurrency("owner", "USD", "VND"), http.StatusOK)
	if same.Body["currencyCode"] != "USD" || same.Body["currencyLocked"] != true {
		test.Fatalf("locked no-op = %s", same.Raw)
	}
	if locked := harness.expect(harness.setCurrency("owner", "EUR", "USD"), http.StatusConflict); locked.Body["code"] != "currency_locked" {
		test.Fatalf("locked = %s", locked.Raw)
	}
	// Locked wins over a stale expected code.
	if locked := harness.expect(harness.setCurrency("owner", "EUR", "VND"), http.StatusConflict); locked.Body["code"] != "currency_locked" {
		test.Fatalf("locked and stale = %s", locked.Raw)
	}
	if got := harness.storedCurrency("owner"); got != (currencyState{"USD", true}) {
		test.Fatalf("after lock = %+v", got)
	}
}

func TestSetCurrencyRejectsBadInput(test *testing.T) {
	harness := newHarness(test)
	harness.sessionOf("owner")
	for name, body := range map[string]string{
		"malformed":   `{"currencyCode":`,
		"trailing":    `{"currencyCode":"USD","expectedCurrencyCode":"VND"}{}`,
		"missing":     `{"currencyCode":"USD"}`,
		"extra":       `{"currencyCode":"USD","expectedCurrencyCode":"VND","id":"x"}`,
		"lower case":  `{"currencyCode":"usd","expectedCurrencyCode":"VND"}`,
		"bad expect":  `{"currencyCode":"USD","expectedCurrencyCode":"vnd"}`,
		"not a code":  `{"currencyCode":"GBP","expectedCurrencyCode":"VND"}`,
		"wrong types": `{"currencyCode":1,"expectedCurrencyCode":true}`,
	} {
		if result := harness.rawAs("owner", body); result.Status != http.StatusBadRequest {
			test.Errorf("%s: status = %d, body %s", name, result.Status, result.Raw)
		}
	}
	if got := harness.storedCurrency("owner"); got != (currencyState{"VND", false}) {
		test.Fatalf("bad input changed the account: %+v", got)
	}
	harness.expect(harness.rawAs("", `{"currencyCode":"USD","expectedCurrencyCode":"VND"}`), http.StatusUnauthorized)
}

func TestCurrencyBelongsToOneAccount(test *testing.T) {
	harness := newHarness(test)
	harness.expect(harness.setCurrency("anna", "EUR", "VND"), http.StatusOK)
	harness.sessionOf("ben")
	if got := harness.storedCurrency("ben"); got != (currencyState{"VND", false}) {
		test.Fatalf("ben = %+v", got)
	}
	if me := harness.expect(harness.callAs("ben", "GET", "/auth/me", nil), http.StatusOK); me.Body["currencyCode"] != "VND" {
		test.Fatalf("ben me = %s", me.Raw)
	}
	// Ben's save names no account; it changes Ben only.
	harness.expect(harness.setCurrency("ben", "USD", "VND"), http.StatusOK)
	if got := harness.storedCurrency("anna"); got != (currencyState{"EUR", false}) {
		test.Fatalf("anna = %+v", got)
	}
}

func TestSaveOnOneServerLeavesAnotherDatabaseUnchanged(test *testing.T) {
	first, second := newHarness(test), newHarness(test)
	var userID string
	if err := first.pool.QueryRow(context.Background(), "SELECT id FROM app_user WHERE email = $1", actorEmail("tester")).Scan(&userID); err != nil {
		test.Fatal(err)
	}
	// The same account id exists on the second server, whose database is separate.
	second.execSQL("INSERT INTO app_user (id, email, password_hash) VALUES ($1, 'twin@example.test', 'unused')", userID)

	first.expect(first.call("PUT", "/auth/me/currency", map[string]string{"currencyCode": "EUR", "expectedCurrencyCode": "VND"}), http.StatusOK)
	if got := first.currencyOf(userID); got != (currencyState{"EUR", false}) {
		test.Fatalf("first database = %+v", got)
	}
	if got := second.currencyOf(userID); got != (currencyState{"VND", false}) {
		test.Fatalf("second database = %+v", got)
	}
}

func TestRegistrationReturnsTheLockAfterTheClaim(test *testing.T) {
	harness := newHarness(test)
	// A priced roll from before accounts existed goes to the next account that signs up.
	harness.addRoll(nil, harness.addStock(nil), 150000)
	browser := harness.browser()
	registered := harness.expect(browser.call("POST", "/auth/register", map[string]string{"email": "claimant@example.com", "password": "Zone-System5"}), http.StatusCreated)
	user := asObject(registered.Body["user"])
	if user["currencyCode"] != "VND" || user["currencyLocked"] != true {
		test.Fatalf("registered user = %v", user)
	}
	if me := harness.expect(browser.call("GET", "/auth/me", nil), http.StatusOK); me.Body["currencyLocked"] != true {
		test.Fatalf("me = %v", me.Body)
	}

	// An account that claims nothing priced stays unlocked.
	other := harness.expect(harness.browser().call("POST", "/auth/register", map[string]string{"email": "plain@example.com", "password": "Zone-System5"}), http.StatusCreated)
	if user := asObject(other.Body["user"]); user["currencyLocked"] != false {
		test.Fatalf("plain user = %v", user)
	}
}

func TestFirstGoogleSignInReturnsTheLockAfterTheClaim(test *testing.T) {
	harness := newHarness(test)
	harness.addRoll(nil, harness.addStock(nil), 150000)
	browser := harness.browser()
	browser.signInWithGoogle()
	if me := harness.expect(browser.call("GET", "/auth/me", nil), http.StatusOK); me.Body["currencyLocked"] != true || me.Body["currencyCode"] != "VND" {
		test.Fatalf("me = %v", me.Body)
	}
}

func TestSignInAndRecoveryLeaveTheCurrencyAlone(test *testing.T) {
	harness := newHarness(test)
	browser := harness.browser()
	harness.expect(browser.call("POST", "/auth/register", map[string]string{"email": "ansel@example.com", "password": "Zone-System5"}), http.StatusCreated)
	harness.expect(browser.call("PUT", "/auth/me/currency", map[string]string{"currencyCode": "EUR", "expectedCurrencyCode": "VND"}), http.StatusOK)
	harness.execSQL("UPDATE app_user SET currency_locked = true WHERE email = 'ansel@example.com'")
	check := func(step string) {
		test.Helper()
		var state currencyState
		err := harness.pool.QueryRow(context.Background(), "SELECT currency_code, currency_locked FROM app_user WHERE email = 'ansel@example.com'").Scan(&state.code, &state.locked)
		if err != nil || state != (currencyState{"EUR", true}) {
			test.Fatalf("%s: currency = %+v err=%v", step, state, err)
		}
	}

	harness.expect(browser.call("POST", "/auth/logout", nil), http.StatusOK)
	login := harness.expect(browser.call("POST", "/auth/login", map[string]string{"email": "ansel@example.com", "password": "Zone-System5"}), http.StatusOK)
	if user := asObject(login.Body["user"]); user["currencyCode"] != "EUR" || user["currencyLocked"] != true {
		test.Fatalf("login user = %v", user)
	}
	check("login")
	if me := harness.expect(browser.call("GET", "/auth/me", nil), http.StatusOK); me.Body["currencyCode"] != "EUR" {
		test.Fatalf("me = %v", me.Body)
	}

	harness.expect(browser.call("POST", "/auth/recover", map[string]string{"email": "ansel@example.com"}), http.StatusOK)
	harness.expect(browser.call("POST", "/auth/recover/end", map[string]string{"token": harness.mail.RecoverToken(), "password": "Fresh-Neg4tive"}), http.StatusOK)
	check("recovery")

	// A first Google sign-in is refused. A Google account linked before MF-22 still signs in and updates its profile.
	harness.google.Account = map[string]any{"sub": "g-1", "email": "ansel@example.com", "email_verified": true, "name": "Ansel Adams", "picture": "https://example.com/a.png"}
	googleBrowser := harness.browser()
	if landing := googleBrowser.signInWithGoogle(); landing != "http://app.test/login?error=account_conflict" {
		test.Fatalf("Google link landed on %q", landing)
	}
	check("google refusal")

	if _, err := harness.pool.Exec(context.Background(), "UPDATE app_user SET oauth2_provider = 'google', oauth2_uid = 'g-1' WHERE email = 'ansel@example.com'"); err != nil {
		test.Fatal(err)
	}
	if landing := harness.browser().signInWithGoogle(); landing != "http://app.test/rolls" {
		test.Fatalf("Google sign-in landed on %q", landing)
	}
	check("google sign-in")

	harness.google.Account["picture"] = "https://example.com/b.png"
	if landing := googleBrowser.signInWithGoogle(); landing != "http://app.test/rolls" {
		test.Fatalf("Google profile update landed on %q", landing)
	}
	me := harness.expect(googleBrowser.call("GET", "/auth/me", nil), http.StatusOK)
	if me.Body["avatarUrl"] != "https://example.com/b.png" || me.Body["currencyCode"] != "EUR" || me.Body["currencyLocked"] != true {
		test.Fatalf("me after Google profile update = %v", me.Body)
	}
	check("google profile update")
}

// amountCases are the 6 amount properties, each with a request that is valid except for the amount.
func amountCases(harness *harness) map[string]func(amount int64) response {
	seeded := seedGear(harness)
	labID := newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	order := func(unitPrice, vat, discount int64) response {
		body := orderBody(labID, newID(), newID(), newID())
		for _, item := range body["items"].([]map[string]any) {
			item["unitPrice"], item["quantity"] = unitPrice, 1
		}
		body["vat"], body["discount"] = vat, discount
		return harness.call("PUT", "/lab-orders/"+newID(), body)
	}
	return map[string]func(int64) response{
		"LabOrderItemInput.unitPrice": func(amount int64) response { return order(amount, 0, 0) },
		"LabOrderInput.vat":           func(amount int64) response { return order(0, amount, 0) },
		"LabOrderInput.discount":      func(amount int64) response { return order(amount, 0, amount) },
		"BulkRollsInput.price": func(amount int64) response {
			return harness.postBulk(newID(), map[string]any{"filmStockId": seeded.stockID, "format": 135, "quantity": 1, "price": amount})
		},
		"RollInput.price": func(amount int64) response {
			return harness.call("PUT", "/rolls/"+harness.addRolls(seeded, 1)[0], map[string]any{"filmStockId": seeded.stockID, "format": 135, "exposures": 36, "price": amount})
		},
		"ProcessingInput.price": func(amount int64) response {
			return harness.call("PUT", "/rolls/"+harness.finishedRoll(seeded)+"/processing/"+newID(), map[string]any{"labId": labID, "type": "develop", "price": amount})
		},
	}
}

func TestAmountsStayInsideTheDatabaseRange(test *testing.T) {
	harness := newHarness(test)
	for name, send := range amountCases(harness) {
		if result := send(2147483647); result.Status != http.StatusCreated && result.Status != http.StatusOK {
			test.Errorf("%s at 2147483647: status = %d, body %s", name, result.Status, result.Raw)
		}
		if result := send(2147483648); result.Status != http.StatusBadRequest {
			test.Errorf("%s at 2147483648: status = %d, body %s", name, result.Status, result.Raw)
		}
	}
}

func TestExpectedCurrencyHeaderIsNotCheckedYet(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.addRolls(seeded, 1)[0]
	// Slice 3 adds the check for price-bearing writes. A price-free write never has one.
	for name, values := range map[string][]string{"invalid": {"usd"}, "empty": {""}, "two lines": {"VND", "USD"}} {
		request := httptest.NewRequest("PUT", "/rolls/"+rollID, bytes.NewReader([]byte(
			`{"filmStockId":"`+seeded.stockID+`","format":135,"exposures":36}`)))
		request.Header.Set("Content-Type", "application/json")
		for _, value := range values {
			request.Header.Add("Expected-Currency", value)
		}
		if result := harness.serve(request); result.Status != http.StatusOK {
			test.Errorf("%s: status = %d, body %s", name, result.Status, result.Raw)
		}
	}
}
