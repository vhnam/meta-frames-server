package integration_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func (harness *harness) count(query string, arguments ...any) int {
	harness.test.Helper()
	var count int
	if err := harness.pool.QueryRow(context.Background(), query, arguments...).Scan(&count); err != nil {
		harness.test.Fatal(err)
	}
	return count
}

func (harness *harness) newLab(name string) string {
	harness.test.Helper()
	labID := newID()
	harness.expect(harness.call("PUT", "/labs/"+labID, map[string]any{"name": name}), http.StatusCreated)
	return labID
}

func handOver(labID string, rolls ...map[string]any) map[string]any {
	return map[string]any{"labId": labID, "handedAt": "2026-10-10", "rolls": rolls}
}

func handed(rollID, jobType string) map[string]any {
	return map[string]any{"rollId": rollID, "type": jobType}
}

func TestHandOverSendsABatchToOneLab(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	developOnly, developScan, another := harness.finishedRoll(seeded), harness.finishedRoll(seeded), harness.finishedRoll(seeded)

	body := handOver(labID, handed(developOnly, "develop"), handed(developScan, "develop_scan"), handed(another, "develop_scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "noritsu"}}
	first := harness.expect(harness.callWith("POST", "/lab-handoffs", map[string]string{"Idempotency-Key": "batch-1"}, body), http.StatusCreated)
	for _, rollID := range []string{developOnly, developScan, another} {
		if roll := harness.rollOf(rollID); roll["stage"] != "processing" || roll["stageDetail"] != "lab_developing" {
			test.Fatalf("handed roll = %v", roll)
		}
	}
	if handoffs := harness.count(`SELECT count(*) FROM lab_handoff WHERE lab_id = $1 AND returned_at IS NULL`, labID); handoffs != 3 {
		test.Fatalf("handoffs = %d", handoffs)
	}
	if batches := harness.count(`SELECT count(DISTINCT batch_id) FROM lab_handoff WHERE lab_id = $1`, labID); batches != 1 {
		test.Fatalf("batches = %d", batches)
	}
	// Each job points at its roll's handoff; only the jobs that produce scans get the scanners.
	linked := `SELECT count(*) FROM processing p JOIN lab_handoff h ON h.id = p.handoff_id AND h.roll_id = p.roll_id
		WHERE p.roll_id = $1 AND p.lab_id = $2 AND p.sent_at = DATE '2026-10-10'`
	scanners := `SELECT count(*) FROM processing_scan_order o JOIN processing p ON p.id = o.processing_id WHERE p.roll_id = $1`
	for rollID, wantScanners := range map[string]int{developOnly: 0, developScan: 1, another: 1} {
		if jobs := harness.count(linked, rollID, labID); jobs != 1 {
			test.Fatalf("roll %s: linked jobs = %d", rollID, jobs)
		}
		if got := harness.count(scanners, rollID); got != wantScanners {
			test.Fatalf("roll %s: scan orders = %d, want %d", rollID, got, wantScanners)
		}
	}

	retry := harness.expect(harness.callWith("POST", "/lab-handoffs", map[string]string{"Idempotency-Key": "batch-1"}, body), http.StatusCreated)
	if !bytes.Equal(retry.Raw, first.Raw) || harness.count(`SELECT count(*) FROM lab_handoff`) != 3 || harness.count(`SELECT count(*) FROM processing`) != 3 {
		test.Fatalf("retry created more: %s", retry.Raw)
	}
}

func TestHandOverSendsWaitingRollsForAScan(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")

	// Developed by a lab, negatives back, planned for a scan: waiting.
	waiting := harness.finishedRoll(seeded)
	developJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+waiting+"/processing/"+developJob, map[string]any{"labId": labID, "type": "develop"}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+developJob+"/negatives-returned", nil), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+waiting+"/plan", map[string]any{"target": "scanned"}), http.StatusOK)
	harness.execSQL(`UPDATE roll SET scan_requested_at = now() WHERE id = $1`, waiting)
	if roll := harness.rollOf(waiting); roll["stageDetail"] != "waiting_scan" {
		test.Fatalf("waiting roll = %v", roll)
	}

	// Scanned and closed, then marked "Cần scan".
	marked := harness.finishedRoll(seeded)
	scanJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+marked+"/processing/"+scanJob, map[string]any{"labId": labID, "type": "develop_scan", "scanOrders": []map[string]any{{"scanner": "noritsu"}}}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+scanJob+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	harness.expect(harness.call("PUT", "/processing/"+scanJob+"/negatives-returned", nil), http.StatusOK)
	harness.execSQL(`UPDATE roll SET scan_requested_at = now() WHERE id = $1`, marked)
	if roll := harness.rollOf(marked); roll["stage"] != "completed" || roll["needsScan"] != true {
		test.Fatalf("marked roll = %v", roll)
	}

	body := handOver(labID, handed(waiting, "scan"), handed(marked, "scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "frontier"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusCreated)
	for _, rollID := range []string{waiting, marked} {
		roll := harness.rollOf(rollID)
		if roll["stage"] != "processing" || roll["needsScan"] != false {
			test.Fatalf("roll sent for a scan = %v", roll)
		}
		if jobs := harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1 AND type = 'scan' AND handoff_id IS NOT NULL`, rollID); jobs != 1 {
			test.Fatalf("roll %s: scan jobs = %d", rollID, jobs)
		}
	}
}

func TestHandOverRefusesTheWholeBatch(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA, labB := harness.newLab("Lab A"), harness.newLab("Lab B")
	ready := harness.finishedRoll(seeded)

	// Lab B develops this one; the negatives are still there.
	atLabB := harness.finishedRoll(seeded)
	harness.expect(harness.call("PUT", "/rolls/"+atLabB+"/processing/"+newID(), map[string]any{"labId": labB, "type": "develop"}), http.StatusCreated)

	// Lab A developed this one and holds the negatives; same-lab scan waits for slice 3c.
	atLabA := harness.finishedRoll(seeded)
	developJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+atLabA+"/processing/"+developJob, map[string]any{"labId": labA, "type": "develop"}), http.StatusCreated)
	harness.execSQL(`UPDATE processing SET developed_at = DATE '2026-10-05' WHERE id = $1`, developJob)
	harness.expect(harness.call("PUT", "/rolls/"+atLabA+"/plan", map[string]any{"target": "scanned", "scanBy": "lab"}), http.StatusOK)

	inStock := harness.addRolls(seeded, 1)[0]
	noScanners := []map[string]any(nil)
	cases := []struct {
		name       string
		rolls      []map[string]any
		scanOrders []map[string]any
		wantStatus int
		wantCode   string
		wantRoll   string
	}{
		{"a roll not waiting", []map[string]any{handed(ready, "develop"), handed(inStock, "develop")}, noScanners, http.StatusConflict, "roll_not_waiting", inStock},
		{"a scan with no scanners", []map[string]any{handed(ready, "develop_scan")}, noScanners, http.StatusUnprocessableEntity, "scan_orders_required", ""},
		{"a roll another lab holds", []map[string]any{handed(ready, "develop"), handed(atLabB, "develop")}, noScanners, http.StatusConflict, "negatives_at_lab", atLabB},
		{"a scan at the lab that holds it", []map[string]any{handed(ready, "develop"), handed(atLabA, "scan")}, []map[string]any{{"scanner": "noritsu"}}, http.StatusConflict, "negatives_at_lab", atLabA},
	}
	for _, testCase := range cases {
		body := handOver(labA, testCase.rolls...)
		if testCase.scanOrders != nil {
			body["scanOrders"] = testCase.scanOrders
		}
		refused := harness.expect(harness.call("POST", "/lab-handoffs", body), testCase.wantStatus)
		message, _ := refused.Body["message"].(string)
		if refused.Body["code"] != testCase.wantCode || (testCase.wantRoll != "" && !strings.Contains(message, testCase.wantRoll)) {
			test.Fatalf("%s: body = %s", testCase.name, refused.Raw)
		}
		if harness.count(`SELECT count(*) FROM lab_handoff`) != 0 || harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1`, ready) != 0 || harness.rollOf(ready)["stage"] != "done_shooting" {
			test.Fatalf("%s: a refused batch saved something", testCase.name)
		}
	}
}

// Until slice 3b no request closes a handoff: a roll back from the lab through the job endpoint
// is refused with a reason, not a unique-index error.
func TestHandOverRefusesARollWithAnOpenHandoff(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	rollID := harness.finishedRoll(seeded)
	harness.expect(harness.call("POST", "/lab-handoffs", handOver(labID, handed(rollID, "develop"))), http.StatusCreated)

	var jobID string
	if err := harness.pool.QueryRow(context.Background(), `SELECT id::text FROM processing WHERE roll_id = $1`, rollID).Scan(&jobID); err != nil {
		test.Fatal(err)
	}
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/negatives-returned", nil), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/plan", map[string]any{"target": "scanned"}), http.StatusOK)

	body := handOver(labID, handed(rollID, "scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "noritsu"}}
	refused := harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusConflict)
	if refused.Body["code"] != "negatives_at_lab" || harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1`, rollID) != 1 {
		test.Fatalf("body = %s", refused.Raw)
	}
}

// "other" creates the account's "Unknown scanner" on first use; a refused batch must not keep it.
func TestRefusedHandOverCreatesNoScanner(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	scanners := harness.count(`SELECT count(*) FROM scanner`)

	body := handOver(labID, handed(harness.addRolls(seeded, 1)[0], "develop_scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "other"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusConflict)
	if got := harness.count(`SELECT count(*) FROM scanner`); got != scanners {
		test.Fatalf("scanners = %d, want %d", got, scanners)
	}

	// A develop-only batch uses no scanner, so it creates none.
	body = handOver(labID, handed(harness.finishedRoll(seeded), "develop"))
	body["scanOrders"] = []map[string]any{{"scanner": "other"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusCreated)
	if got := harness.count(`SELECT count(*) FROM scanner`); got != scanners {
		test.Fatalf("after a develop-only batch: scanners = %d, want %d", got, scanners)
	}

	body = handOver(labID, handed(harness.finishedRoll(seeded), "develop_scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "other"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusCreated)
	if got := harness.count(`SELECT count(*) FROM scanner`); got != scanners+1 {
		test.Fatalf("after an accepted batch: scanners = %d, want %d", got, scanners+1)
	}
}
