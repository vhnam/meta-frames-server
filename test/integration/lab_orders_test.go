package integration_test

import (
	"net/http"
	"testing"
)

// orderBody is the order the lab issued: one B&W roll and three colour rolls, plus VAT.
func orderBody(labID, code, bwLine, colourLine string) map[string]any {
	return map[string]any{
		"labId": labID, "code": code, "orderedAt": "2026-10-05T10:03:00+07:00", "dueDate": "2026-10-20", "vat": 44800,
		"items": []map[string]any{
			{"id": bwLine, "product": "35mm Black & White film", "options": "Development + Scan", "quantity": 1, "unitPrice": 125000},
			{"id": colourLine, "product": "35mm Colour film", "options": "Development + Scan", "quantity": 3, "unitPrice": 145000},
		},
	}
}

func sendRoll(harness *harness, rollID, labID string) string {
	harness.test.Helper()
	jobID := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop"}), http.StatusCreated)
	return jobID
}

func TestLabOrderSetsThePriceOfItsJobs(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID, orderID, bwLine, colourLine := newID(), newID(), newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)

	created := harness.expect(harness.call("PUT", "/lab-orders/"+orderID, orderBody(labID, "0510006", bwLine, colourLine)), http.StatusCreated)
	if created.Body["subtotal"] != float64(560000) || created.Body["total"] != float64(604800) || created.Body["labName"] != "Lab A" {
		test.Fatalf("order = %s", created.Raw)
	}
	lines := asList(created.Body["items"])
	if asObject(lines[0])["unitCost"] != float64(135000) || asObject(lines[1])["unitCost"] != float64(156600) || asObject(lines[1])["lineTotal"] != float64(435000) {
		test.Fatalf("lines = %s", created.Raw)
	}

	// From Processing: the job is sent on the line, and its price comes from there.
	rollID := harness.finishedRoll(seeded)
	jobID := newID()
	path := "/rolls/" + rollID + "/processing/" + jobID
	harness.expect(harness.call("PUT", path, map[string]any{"labId": labID, "type": "develop", "orderItemIds": []string{colourLine}, "price": 1}), http.StatusUnprocessableEntity)
	harness.expect(harness.call("PUT", path, map[string]any{"type": "develop", "orderItemIds": []string{colourLine}}), http.StatusUnprocessableEntity)
	sent := harness.expect(harness.call("PUT", path, map[string]any{"labId": labID, "type": "develop", "orderItemIds": []string{colourLine}}), http.StatusCreated)
	if sent.Body["price"] != float64(156600) || asObject(sent.Body["order"])["code"] != "0510006" {
		test.Fatalf("job = %s", sent.Raw)
	}
	detail := harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK)
	if totals := asObject(detail.Body["totals"]); totals["processingPrice"] != float64(156600) {
		test.Fatalf("totals = %s", detail.Raw)
	}

	// From the order: a job with its own price joins a line and loses that price.
	otherRoll := harness.finishedRoll(seeded)
	otherJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+otherRoll+"/processing/"+otherJob, map[string]any{"labId": labID, "type": "develop", "price": 7}), http.StatusCreated)
	if linkable := harness.listOf("/lab-orders/" + orderID + "/linkable-jobs"); len(linkable) != 2 {
		test.Fatalf("linkable = %v", linkable)
	}
	linked := harness.expect(harness.call("PUT", "/processing/"+otherJob+"/order-items", map[string]any{"orderItemIds": []string{bwLine}}), http.StatusOK)
	if linked.Body["price"] != float64(135000) {
		test.Fatalf("linked = %s", linked.Raw)
	}
	order := harness.expect(harness.call("GET", "/lab-orders/"+orderID, nil), http.StatusOK)
	if jobs := asList(asObject(asList(order.Body["items"])[0])["jobs"]); len(jobs) != 1 || asObject(jobs[0])["rollId"] != otherRoll {
		test.Fatalf("order = %s", order.Raw)
	}

	// The line for one roll is full now; saving the job again on its own line is not a new place.
	thirdJob := sendRoll(harness, harness.finishedRoll(seeded), labID)
	harness.expect(harness.call("PUT", "/processing/"+thirdJob+"/order-items", map[string]any{"orderItemIds": []string{bwLine}}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/processing/"+otherJob+"/order-items", map[string]any{"orderItemIds": []string{bwLine}}), http.StatusOK)

	// Spending follows the shared-out price: the job with no price yet only marks it incomplete.
	if spending := harness.listOf("/stats/spending"); len(spending) != 1 || spending[0]["processingCost"] != float64(291600) || spending[0]["incomplete"] != true {
		test.Fatalf("spending = %v", spending)
	}

	// Lines and orders in use stay; taking the jobs off frees them.
	harness.expect(harness.call("DELETE", "/lab-orders/"+orderID, nil), http.StatusConflict)
	otherLab := newID()
	harness.expect(harness.call("PUT", "/labs/"+otherLab, map[string]any{"name": "Lab B"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lab-orders/"+orderID, orderBody(otherLab, "0510006", bwLine, colourLine)), http.StatusConflict)
	harness.expect(harness.call("DELETE", "/labs/"+labID, nil), http.StatusConflict)
	dropped := orderBody(labID, "0510006", bwLine, colourLine)
	dropped["items"] = dropped["items"].([]map[string]any)[1:]
	harness.expect(harness.call("PUT", "/lab-orders/"+orderID, dropped), http.StatusConflict)

	cleared := harness.expect(harness.call("PUT", "/processing/"+otherJob+"/order-items", map[string]any{"orderItemIds": []string{}}), http.StatusOK)
	if _, hasPrice := cleared.Body["price"]; hasPrice || cleared.Body["order"] != nil {
		test.Fatalf("cleared = %s", cleared.Raw)
	}
	harness.expect(harness.call("PUT", "/lab-orders/"+orderID, dropped), http.StatusOK)

	// A deleted job lets go of its line, and the order can then go too.
	harness.expect(harness.call("DELETE", "/processing/"+jobID, nil), http.StatusNoContent)
	harness.expect(harness.call("DELETE", "/lab-orders/"+orderID, nil), http.StatusNoContent)
	harness.expect(harness.call("GET", "/lab-orders/"+orderID, nil), http.StatusNotFound)
	if orders := harness.listOf("/lab-orders"); len(orders) != 0 {
		test.Fatalf("orders = %v", orders)
	}
}

func TestOneRollOnTwoLinesOfAnOrder(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID, developLine, scanLine, otherLine := newID(), newID(), newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	orderID := newID()
	harness.expect(harness.call("PUT", "/lab-orders/"+orderID, map[string]any{
		"labId": labID, "code": "1", "orderedAt": "2026-10-05T10:03:00+07:00", "vat": 10000,
		"items": []map[string]any{
			{"id": developLine, "product": "Develop", "quantity": 1, "unitPrice": 60000},
			{"id": scanLine, "product": "Scan", "quantity": 1, "unitPrice": 40000},
		},
	}), http.StatusCreated)
	otherOrder := newID()
	harness.expect(harness.call("PUT", "/lab-orders/"+otherOrder, orderBody(labID, "2", otherLine, newID())), http.StatusCreated)

	// The roll's price adds up its lines; its roll counts once on each.
	rollID := harness.finishedRoll(seeded)
	jobID := sendRoll(harness, rollID, labID)
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/order-items", map[string]any{"orderItemIds": []string{developLine, otherLine}}), http.StatusUnprocessableEntity)
	linked := harness.expect(harness.call("PUT", "/processing/"+jobID+"/order-items", map[string]any{"orderItemIds": []string{developLine}}), http.StatusOK)
	if linked.Body["price"] != float64(66000) {
		test.Fatalf("one line = %s", linked.Raw)
	}
	// Already on this order, the job can still take another line; another order does not offer it.
	if linkable := harness.listOf("/lab-orders/" + orderID + "/linkable-jobs"); len(linkable) != 1 || len(asList(linkable[0]["orderItemIds"])) != 1 {
		test.Fatalf("linkable = %v", linkable)
	}
	if linkable := harness.listOf("/lab-orders/" + otherOrder + "/linkable-jobs"); len(linkable) != 0 {
		test.Fatalf("other order offers %v", linkable)
	}
	both := harness.expect(harness.call("PUT", "/processing/"+jobID+"/order-items", map[string]any{"orderItemIds": []string{developLine, scanLine, scanLine}}), http.StatusOK)
	if both.Body["price"] != float64(110000) || len(asList(asObject(both.Body["order"])["items"])) != 2 {
		test.Fatalf("two lines = %s", both.Raw)
	}
	detail := harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK)
	if totals := asObject(detail.Body["totals"]); totals["processingPrice"] != float64(110000) {
		test.Fatalf("totals = %s", detail.Raw)
	}
	if spending := harness.listOf("/stats/spending"); spending[0]["processingCost"] != float64(110000) {
		test.Fatalf("spending = %v", spending)
	}

	// Both lines are full; a second roll gets neither, and a job saved again keeps its places.
	second := sendRoll(harness, harness.finishedRoll(seeded), labID)
	harness.expect(harness.call("PUT", "/processing/"+second+"/order-items", map[string]any{"orderItemIds": []string{scanLine}}), http.StatusConflict)
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/order-items", map[string]any{"orderItemIds": []string{developLine, scanLine}}), http.StatusOK)

	// Taking one line off frees that line only; a deleted job frees the rest.
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/order-items", map[string]any{"orderItemIds": []string{developLine}}), http.StatusOK)
	harness.expect(harness.call("PUT", "/processing/"+second+"/order-items", map[string]any{"orderItemIds": []string{scanLine}}), http.StatusOK)
	harness.expect(harness.call("DELETE", "/processing/"+jobID, nil), http.StatusNoContent)
	harness.expect(harness.call("PUT", "/processing/"+second+"/order-items", map[string]any{"orderItemIds": []string{}}), http.StatusOK)
	harness.expect(harness.call("DELETE", "/lab-orders/"+orderID, nil), http.StatusNoContent)
	if trail := harness.auditTrail("entityType=processing_order_item"); len(trail) == 0 {
		test.Fatal("link changes are not audited")
	}
}

func TestLabOrderRulesOnItsLines(test *testing.T) {
	harness := newHarness(test)
	labID, orderID, bwLine, colourLine := newID(), newID(), newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lab-orders/"+newID(), orderBody(newID(), "1", newID(), newID())), http.StatusUnprocessableEntity)

	harness.expect(harness.call("PUT", "/lab-orders/"+orderID, orderBody(labID, "A-1", bwLine, colourLine)), http.StatusCreated)
	// a code is unique per lab, ignoring case; a line cannot move in from another order
	harness.expect(harness.call("PUT", "/lab-orders/"+newID(), orderBody(labID, "a-1", newID(), newID())), http.StatusConflict)
	harness.expect(harness.call("PUT", "/lab-orders/"+newID(), orderBody(labID, "A-2", bwLine, newID())), http.StatusConflict)

	tooMuch := orderBody(labID, "A-3", newID(), newID())
	tooMuch["discount"] = 700000
	harness.expect(harness.call("PUT", "/lab-orders/"+newID(), tooMuch), http.StatusUnprocessableEntity)
	same := newID()
	twice := orderBody(labID, "A-4", same, same)
	harness.expect(harness.call("PUT", "/lab-orders/"+newID(), twice), http.StatusUnprocessableEntity)
}

func TestLabOrdersBelongToOneAccount(test *testing.T) {
	harness := newHarness(test)
	labID, orderID := newID(), newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": "Lab A"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/lab-orders/"+orderID, orderBody(labID, "A-1", newID(), newID())), http.StatusCreated)
	if orders := harness.listAs("stranger", "/lab-orders"); len(orders) != 0 {
		test.Fatalf("orders = %v", orders)
	}
	harness.expect(harness.callAs("stranger", "GET", "/lab-orders/"+orderID, nil), http.StatusNotFound)
	harness.expect(harness.callAs("stranger", "DELETE", "/lab-orders/"+orderID, nil), http.StatusNotFound)
}
