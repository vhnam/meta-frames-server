package integration_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"meta-frames-server/internal/db/gen"
	"meta-frames-server/migrations"
)

// currencyState is what an account holds after migration 21.
type currencyState struct {
	code   string
	locked bool
}

func (harness *harness) currencyOf(userID string) currencyState {
	harness.test.Helper()
	var state currencyState
	err := harness.pool.QueryRow(context.Background(),
		"SELECT currency_code, currency_locked FROM app_user WHERE id = $1", userID).Scan(&state.code, &state.locked)
	if err != nil {
		harness.test.Fatal(err)
	}
	return state
}

// fingerprints hashes every table and view the migration must leave alone.
func (harness *harness) fingerprints() map[string]string {
	harness.test.Helper()
	sources := map[string]string{
		"roll": "id", "processing": "id", "lab_order": "id", "lab_order_item": "id", "audit_log": "id",
		"lab_order_item_cost": "item_id", "processing_order_cost": "processing_id",
	}
	hashes := map[string]string{}
	for source, key := range sources {
		var hash string
		query := "SELECT md5(coalesce(string_agg(source::text, '|' ORDER BY source." + key + "), '')) FROM " + source + " AS source"
		if err := harness.pool.QueryRow(context.Background(), query).Scan(&hash); err != nil {
			harness.test.Fatal(err)
		}
		hashes[source] = hash
	}
	return hashes
}

func (harness *harness) addUser(name string) string {
	harness.test.Helper()
	userID := newID()
	harness.execSQL("INSERT INTO app_user (id, email, password_hash) VALUES ($1, $2, 'unused')", userID, actorEmail(name))
	return userID
}

func (harness *harness) addStock(ownerID any) string {
	harness.test.Helper()
	stockID := newID()
	harness.execSQL(`INSERT INTO film_stock (id, brand, name, type, box_iso, process, packaging, owner_id)
		VALUES ($1, 'Kodak', 'Gold', 'color', 200, 'C-41', 'factory', $2)`, stockID, ownerID)
	return stockID
}

func (harness *harness) addRoll(ownerID any, stockID string, price any) string {
	harness.test.Helper()
	rollID := newID()
	harness.execSQL("INSERT INTO roll (id, film_stock_id, format, exposures, price, owner_id) VALUES ($1, $2, 135, 36, $3, $4)",
		rollID, stockID, price, ownerID)
	return rollID
}

func (harness *harness) addJob(ownerID any, rollID string, price any) string {
	harness.test.Helper()
	jobID := newID()
	harness.execSQL("INSERT INTO processing (id, roll_id, type, process, sent_at, price, owner_id) VALUES ($1, $2, 'develop', 'C-41', '2026-01-01', $3, $4)",
		jobID, rollID, price, ownerID)
	return jobID
}

// migrateTo moves the harness database to a goose version through the same files the server embeds.
func (harness *harness) migrateTo(version int64, down bool) {
	harness.test.Helper()
	database := stdlib.OpenDB(*harness.pool.Config().ConnConfig)
	defer func() { _ = database.Close() }()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		harness.test.Fatal(err)
	}
	var err error
	if down {
		err = goose.DownTo(database, ".", version)
	} else {
		err = goose.UpTo(database, ".", version)
	}
	if err != nil {
		harness.test.Fatal(err)
	}
}

func gooseVersion(test *testing.T, database *sql.DB) int64 {
	test.Helper()
	version, err := goose.GetDBVersion(database)
	if err != nil {
		test.Fatal(err)
	}
	return version
}

func TestMigrationLocksAccountsWithMoneyEvidence(test *testing.T) {
	harness := newHarness(test)
	test.Cleanup(func() { goose.SetBaseFS(nil) })

	// Back to version 20, then seed the data the way version 20 shaped it.
	harness.migrateTo(20, true)
	database := stdlib.OpenDB(*harness.pool.Config().ConnConfig)
	defer func() { _ = database.Close() }()
	if version := gooseVersion(test, database); version != 20 {
		test.Fatalf("version = %d, want 20", version)
	}

	testerID := ""
	if err := harness.pool.QueryRow(context.Background(), "SELECT id FROM app_user WHERE email = $1", actorEmail("tester")).Scan(&testerID); err != nil {
		test.Fatal(err)
	}

	// tester: a lab order with rounding, and a job priced from its lines. Evidence through lab_order.
	labID, orderID, lineBW, lineColour, lineOdd := newID(), newID(), newID(), newID(), newID()
	harness.execSQL("INSERT INTO lab (id, name, owner_id) VALUES ($1, 'Lab A', $2)", labID, testerID)
	harness.execSQL("INSERT INTO lab_order (id, owner_id, lab_id, code, ordered_at, vat, discount) VALUES ($1, $2, $3, '0510006', now(), 44800, 123)", orderID, testerID, labID)
	for position, line := range []struct {
		id       string
		quantity int
		price    int
	}{{lineBW, 1, 125000}, {lineColour, 3, 145000}, {lineOdd, 1, 33333}} {
		harness.execSQL("INSERT INTO lab_order_item (id, owner_id, order_id, product, quantity, unit_price, position) VALUES ($1, $2, $3, 'film', $4, $5, $6)",
			line.id, testerID, orderID, line.quantity, line.price, position)
	}
	testerRoll := harness.addRoll(testerID, harness.addStock(testerID), nil)
	testerJob := harness.addJob(testerID, testerRoll, nil)
	harness.execSQL("INSERT INTO processing_order_item (processing_id, order_item_id) VALUES ($1, $2), ($1, $3)", testerJob, lineColour, lineOdd)

	// Accounts with evidence, one kind each.
	pricedRoll := harness.addUser("priced-roll")
	harness.addRoll(pricedRoll, harness.addStock(pricedRoll), 100)

	zeroJob := harness.addUser("zero-job")
	zeroStock := harness.addStock(zeroJob)
	harness.addJob(zeroJob, harness.addRoll(zeroJob, zeroStock, nil), 0)

	softDeleted := harness.addUser("soft-deleted")
	deletedRoll := harness.addRoll(softDeleted, harness.addStock(softDeleted), 7)
	harness.execSQL("UPDATE roll SET deleted_at = now() WHERE id = $1", deletedRoll)
	harness.execSQL("DELETE FROM audit_log WHERE entity_type = 'roll' AND entity_id = $1", deletedRoll)

	// Only the clearing audit entry's before snapshot still shows money.
	cleared := harness.addUser("cleared")
	clearedRoll := harness.addRoll(cleared, harness.addStock(cleared), 5)
	harness.execSQL("UPDATE roll SET price = NULL WHERE id = $1", clearedRoll)
	harness.execSQL("DELETE FROM audit_log WHERE entity_type = 'roll' AND entity_id = $1 AND action = 'create'", clearedRoll)

	// A deleted order line, with no audit entry for the order itself.
	deletedLine := harness.addUser("deleted-line")
	harness.execSQL(`INSERT INTO audit_log (entity_type, entity_id, action, before, owner_id, actor)
		VALUES ('lab_order_item', $1, 'delete', '{"unit_price": 90000}', $2, 'nobody@example.test')`, newID(), deletedLine)
	archivedOrder := harness.addUser("archived-order")
	harness.execSQL(`INSERT INTO audit_log (entity_type, entity_id, action, after, owner_id)
		VALUES ('lab_order', $1, 'create', '{"vat": 0, "discount": 0}', $2)`, newID(), archivedOrder)

	// Accounts without evidence: null prices only, and nothing at all. The audit actor of an entry
	// must not decide ownership: this entry names a locked account as actor.
	nullPrices := harness.addUser("null-prices")
	nullRoll := harness.addRoll(nullPrices, harness.addStock(nullPrices), nil)
	harness.addJob(nullPrices, nullRoll, nil)
	harness.execSQL(`INSERT INTO audit_log (entity_type, entity_id, action, after, owner_id, actor)
		VALUES ('roll', $1, 'update', '{"price": null}', $2, $3)`, newID(), nullPrices, actorEmail("priced-roll"))
	harness.execSQL(`INSERT INTO audit_log (entity_type, entity_id, action, before, after, owner_id)
		VALUES ('lab_order_item', $1, 'update', '{"unit_price": null}', '{}', $2)`, newID(), nullPrices)
	missingSnapshots := harness.addUser("missing-order-snapshots")
	harness.execSQL(`INSERT INTO audit_log (entity_type, entity_id, action, before, after, owner_id)
		VALUES ('lab_order', $1, 'update', 'null', NULL, $2)`, newID(), missingSnapshots)
	empty := harness.addUser("empty")

	// Ownerless history: audit entries with a price and no live row.
	harness.execSQL(`INSERT INTO audit_log (entity_type, entity_id, action, before, owner_id)
		VALUES ('processing', $1, 'delete', '{"price": 0}', NULL)`, newID())

	// Amounts as version 20 reports them.
	type amount struct {
		itemID string
		cost   int
		price  int
		total  int
	}
	var before []amount
	rows, err := harness.pool.Query(context.Background(), `SELECT item.id, cost.unit_cost, item.unit_price, item.quantity * item.unit_price
		FROM lab_order_item item JOIN lab_order_item_cost cost ON cost.item_id = item.id
		WHERE item.order_id = $1 ORDER BY item.id`, orderID)
	if err != nil {
		test.Fatal(err)
	}
	for rows.Next() {
		var line amount
		if err := rows.Scan(&line.itemID, &line.cost, &line.price, &line.total); err != nil {
			test.Fatal(err)
		}
		before = append(before, line)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		test.Fatal(err)
	}
	var vatBefore, discountBefore int
	if err := harness.pool.QueryRow(context.Background(), "SELECT vat, discount FROM lab_order WHERE id = $1", orderID).
		Scan(&vatBefore, &discountBefore); err != nil {
		test.Fatal(err)
	}
	rollBefore, err := gen.New(harness.pool).RollSpend(context.Background(), gen.RollSpendParams{
		ID: uuid.MustParse(testerRoll), OwnerID: uuid.MustParse(testerID),
	})
	if err != nil {
		test.Fatal(err)
	}
	var jobCostBefore int
	if err := harness.pool.QueryRow(context.Background(), "SELECT cost FROM processing_order_cost WHERE processing_id = $1", testerJob).Scan(&jobCostBefore); err != nil {
		test.Fatal(err)
	}
	if len(before) != 3 || jobCostBefore == 0 {
		test.Fatalf("seed did not produce order amounts: %v, job %d", before, jobCostBefore)
	}
	hashesBefore := harness.fingerprints()

	harness.migrateTo(21, false)
	if version := gooseVersion(test, database); version != 21 {
		test.Fatalf("version = %d, want 21", version)
	}

	if after := harness.fingerprints(); len(after) != len(hashesBefore) {
		test.Fatalf("fingerprints = %v", after)
	} else {
		for source, hash := range hashesBefore {
			if after[source] != hash {
				test.Errorf("%s changed by the migration", source)
			}
		}
	}

	wantLocked := map[string]bool{
		testerID: true, pricedRoll: true, zeroJob: true, softDeleted: true, cleared: true, deletedLine: true, archivedOrder: true,
		nullPrices: false, empty: false,
	}
	for userID, locked := range wantLocked {
		if got := harness.currencyOf(userID); got != (currencyState{"VND", locked}) {
			test.Errorf("account %s = %+v, want VND locked=%v", userID, got, locked)
		}
	}
	if got := harness.currencyOf(missingSnapshots); got != (currencyState{"VND", false}) {
		test.Errorf("lab-order audit entry without a snapshot: got %+v, want VND unlocked", got)
	}

	// The API reports the same amounts as version 20 did.
	order := harness.expect(harness.call("GET", "/lab-orders/"+orderID, nil), http.StatusOK)
	byLine := map[string]map[string]any{}
	for _, line := range asList(order.Body["items"]) {
		item := asObject(line)
		byLine[item["id"].(string)] = item
	}
	subtotalBefore := 0
	for _, line := range before {
		subtotalBefore += line.total
		item := byLine[line.itemID]
		if item["unitCost"] != float64(line.cost) || item["unitPrice"] != float64(line.price) || item["lineTotal"] != float64(line.total) {
			test.Errorf("line %s amounts = %v, want unitCost %d, unitPrice %d, lineTotal %d", line.itemID, item, line.cost, line.price, line.total)
		}
	}
	if order.Body["subtotal"] != float64(subtotalBefore) || order.Body["vat"] != float64(vatBefore) ||
		order.Body["discount"] != float64(discountBefore) || order.Body["total"] != float64(subtotalBefore+vatBefore-discountBefore) {
		test.Errorf("order amounts = %s, want subtotal %d, VAT %d, discount %d", order.Raw, subtotalBefore, vatBefore, discountBefore)
	}
	roll := harness.expect(harness.call("GET", "/rolls/"+testerRoll, nil), http.StatusOK)
	totals := asObject(roll.Body["totals"])
	if totals["rollPrice"] != float64(rollBefore.RollPrice) || totals["processingPrice"] != float64(rollBefore.ProcessingPrice) ||
		totals["total"] != float64(rollBefore.RollPrice+rollBefore.ProcessingPrice) ||
		totals["incomplete"] != (rollBefore.RollPriceMissing || rollBefore.ProcessingPriceMissing) {
		test.Errorf("roll totals = %v, want version 20 amounts %+v", totals, rollBefore)
	}
	jobPrice := -1.0
	for _, job := range harness.listOf("/rolls/" + testerRoll + "/processing") {
		if job["id"] == testerJob {
			jobPrice, _ = job["price"].(float64)
		}
	}
	if jobPrice != float64(jobCostBefore) {
		test.Errorf("job price = %v, want %d", jobPrice, jobCostBefore)
	}

	// Claims. Each claim takes every ownerless row, so the cases run in this order.
	claim := func(name string) string {
		claimant := harness.addUser(name)
		harness.execSQL("SELECT claim_unowned_data($1)", claimant)
		return claimant
	}
	// The ownerless audit entry above is audit-only money history.
	if got := harness.currencyOf(claim("claim-audit-only")); !got.locked {
		test.Errorf("claim of audit-only money history left the claimant unlocked: %+v", got)
	}
	harness.execSQL("INSERT INTO camera (id, brand, model, mount) VALUES ($1, 'Leica', 'M3', 'M')", uuid.NewString())
	if got := harness.currencyOf(claim("claim-no-money")); got != (currencyState{"VND", false}) {
		test.Errorf("claim with no money = %+v, want VND unlocked", got)
	}
	ownerlessStock := newID()
	harness.execSQL(`INSERT INTO film_stock (id, brand, name, type, box_iso, process, packaging)
		VALUES ($1, 'Kodak', 'Gold', 'color', 200, 'C-41', 'factory')`, ownerlessStock)
	harness.addRoll(nil, ownerlessStock, 120000)
	if got := harness.currencyOf(claim("claim-priced-roll")); !got.locked {
		test.Errorf("claim of a priced roll left the claimant unlocked: %+v", got)
	}

	// Down and up again on the same data.
	harness.migrateTo(20, true)
	if version := gooseVersion(test, database); version != 20 {
		test.Fatalf("version after down = %d, want 20", version)
	}
	harness.migrateTo(21, false)
	if got := harness.currencyOf(testerID); !got.locked {
		test.Errorf("tester after up again = %+v, want locked", got)
	}
	if got := harness.currencyOf(empty); got.locked {
		test.Errorf("empty account after up again = %+v, want unlocked", got)
	}
}
