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

// jobOf returns the id of the roll's only live job of that type.
func (harness *harness) jobOf(rollID, jobType string) string {
	harness.test.Helper()
	var jobID string
	if err := harness.pool.QueryRow(context.Background(), `SELECT id::text FROM processing WHERE roll_id = $1 AND type = $2 AND deleted_at IS NULL`, rollID, jobType).Scan(&jobID); err != nil {
		harness.test.Fatal(err)
	}
	return jobID
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

	// Scanned and closed, then marked "Needs scan".
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
		if harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, ready) != 0 || harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1`, ready) != 0 || harness.rollOf(ready)["stage"] != "done_shooting" {
			test.Fatalf("%s: a refused batch saved something", testCase.name)
		}
	}
}

// A return through the job endpoint closes the handoff, so the roll can go again.
func TestHandOverAcceptsARollBackFromTheLab(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	rollID := harness.finishedRoll(seeded)
	harness.expect(harness.call("POST", "/lab-handoffs", handOver(labID, handed(rollID, "develop"))), http.StatusCreated)

	jobID := harness.jobOf(rollID, "develop")
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/negatives-returned", map[string]any{"date": "2026-10-12"}), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/plan", map[string]any{"target": "scanned"}), http.StatusOK)

	body := handOver(labID, handed(rollID, "scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "noritsu"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusCreated)
	if open := harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1 AND returned_at IS NULL`, rollID); open != 1 || harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, rollID) != 2 {
		test.Fatalf("open handoffs = %d", open)
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

// D8.1 and D8.4: lab jobs share the roll's stay at the lab, and one return brings all of it back.
func TestLabJobsShareOneHandoffUntilTheRollComesBack(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	rollID := harness.finishedRoll(seeded)

	firstJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+firstJob, map[string]any{
		"labId": labID, "type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.execSQL(`UPDATE processing SET developed_at = DATE '2026-10-03' WHERE id = $1`, firstJob)
	harness.expect(harness.call("PUT", "/processing/"+firstJob+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	roll := harness.rollOf(rollID)
	if roll["stage"] != "completed" || roll["negativesAtLab"] != true || roll["openHandoffId"] == nil {
		test.Fatalf("scanned, negatives at the lab = %v", roll)
	}
	handoffID := roll["openHandoffId"].(string)

	// A rescan at the same lab joins the stay.
	secondJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+secondJob, map[string]any{
		"labId": labID, "type": "scan", "sentAt": "2026-10-06", "scanOrders": []map[string]any{{"scanner": "frontier"}},
	}), http.StatusCreated)
	if joined := harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1 AND handoff_id = $2`, rollID, handoffID); joined != 2 || harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, rollID) != 1 {
		test.Fatalf("jobs in the handoff = %d", joined)
	}

	harness.expect(harness.call("PUT", "/processing/"+firstJob+"/negatives-returned", map[string]any{"date": "2026-10-10"}), http.StatusOK)
	roll = harness.rollOf(rollID)
	if roll["negativesAtLab"] != false || roll["openHandoffId"] != nil {
		test.Fatalf("after the return = %v", roll)
	}
	if dated := harness.count(`SELECT count(*) FROM processing WHERE handoff_id = $1 AND negatives_returned_at = DATE '2026-10-10'`, handoffID); dated != 2 {
		test.Fatalf("jobs with the return date = %d", dated)
	}
	if harness.count(`SELECT count(*) FROM lab_handoff WHERE id = $1 AND returned_at = DATE '2026-10-10'`, handoffID) != 1 {
		test.Fatal("the handoff is not closed on the return date")
	}
}

// D8.4: an old return request sent again cannot close a newer stay at the lab.
func TestRepeatedReturnLeavesANewerHandoffOpen(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	rollID := harness.finishedRoll(seeded)

	octoberJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+octoberJob, map[string]any{"labId": labID, "type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	octoberReturn := map[string]any{"date": "2026-10-10"}
	harness.expect(harness.call("PUT", "/processing/"+octoberJob+"/negatives-returned", octoberReturn), http.StatusOK)

	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{
		"labId": labID, "type": "scan", "sentAt": "2026-11-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+octoberJob+"/negatives-returned", octoberReturn), http.StatusOK)

	if roll := harness.rollOf(rollID); roll["negativesAtLab"] != true || roll["openHandoffId"] == nil {
		test.Fatalf("November stay = %v", roll)
	}
	if open := harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1 AND returned_at IS NULL AND handed_at = DATE '2026-11-01'`, rollID); open != 1 {
		test.Fatalf("open November handoffs = %d", open)
	}
}

// D8.5: cancelling the last service keeps the stay; the handoff return still brings the roll back.
func TestReturnLabHandoffWithNoLiveJob(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")

	rollID := harness.finishedRoll(seeded)
	scanJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+scanJob, map[string]any{
		"type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+scanJob+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	harness.execSQL(`UPDATE roll SET scan_requested_at = now() WHERE id = $1`, rollID)

	body := handOver(labID, handed(rollID, "scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "frontier"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusCreated)
	harness.expect(harness.call("DELETE", "/processing/"+harness.jobOf(rollID, "scan"), nil), http.StatusNoContent)
	roll := harness.rollOf(rollID)
	if roll["stage"] != "completed" || roll["negativesAtLab"] != true || roll["openHandoffId"] == nil {
		test.Fatalf("after cancelling the scan = %v", roll)
	}
	handoffID := roll["openHandoffId"].(string)

	tooEarly := harness.expect(harness.call("PUT", "/lab-handoffs/"+handoffID+"/returned", map[string]any{"date": "2026-10-01"}), http.StatusUnprocessableEntity)
	if tooEarly.Body["code"] != "returned_before_handed" {
		test.Fatalf("early return = %s", tooEarly.Raw)
	}
	returned := harness.expect(harness.call("PUT", "/lab-handoffs/"+handoffID+"/returned", map[string]any{"date": "2026-10-12"}), http.StatusOK)
	if returned.Body["negativesAtLab"] != false || returned.Body["openHandoffId"] != nil {
		test.Fatalf("returned roll = %s", returned.Raw)
	}
	again := harness.expect(harness.call("PUT", "/lab-handoffs/"+handoffID+"/returned", map[string]any{"date": "2026-10-20"}), http.StatusOK)
	if !bytes.Equal(again.Raw, returned.Raw) || harness.count(`SELECT count(*) FROM lab_handoff WHERE id = $1 AND returned_at = DATE '2026-10-12'`, handoffID) != 1 {
		test.Fatalf("second return changed the roll: %s", again.Raw)
	}
	harness.expect(harness.call("PUT", "/lab-handoffs/"+newID()+"/returned", nil), http.StatusNotFound)
}

// D8.7: an old lab job with no handoff still means the lab holds the roll.
func TestOldLabJobWithNoHandoffKeepsTheRollAtTheLab(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	rollID := harness.finishedRoll(seeded)
	jobID := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	// what a job from before handoffs looks like
	harness.execSQL(`UPDATE processing SET handoff_id = NULL WHERE id = $1`, jobID)
	harness.execSQL(`DELETE FROM lab_handoff WHERE roll_id = $1`, rollID)

	if roll := harness.rollOf(rollID); roll["negativesAtLab"] != true || roll["openHandoffId"] != nil {
		test.Fatalf("old job roll = %v", roll)
	}
	listed := false
	for _, item := range harness.listOf("/negatives-at-lab") {
		listed = listed || item["processingId"] == jobID
	}
	if !listed {
		test.Fatal("the old job is missing from the negatives-at-lab report")
	}
}

// D8.1: while lab A holds the roll, a job at lab B is refused, not a second open handoff.
func TestLabJobIsRefusedWhileAnotherLabHoldsTheRoll(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA, labB := harness.newLab("Lab A"), harness.newLab("Lab B")
	rollID := harness.finishedRoll(seeded)

	jobID := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{
		"labId": labA, "type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)

	refused := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{
		"labId": labB, "type": "scan", "sentAt": "2026-10-06", "scanOrders": []map[string]any{{"scanner": "frontier"}},
	}), http.StatusConflict)
	if refused.Body["code"] != "negatives_at_lab" || harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, rollID) != 1 ||
		harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1`, rollID) != 1 {
		test.Fatalf("job at lab B = %s", refused.Raw)
	}
}

// D8: an old lab job with no handoff holds the roll like an open handoff does.
func TestOldLabJobHoldsTheRollForNewLabJobs(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA, labB := harness.newLab("Lab A"), harness.newLab("Lab B")
	rollID := harness.finishedRoll(seeded)
	oldJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+oldJob, map[string]any{
		"labId": labA, "type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+oldJob+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	// what a job from before handoffs looks like
	harness.execSQL(`UPDATE processing SET handoff_id = NULL WHERE id = $1`, oldJob)
	harness.execSQL(`DELETE FROM lab_handoff WHERE roll_id = $1`, rollID)

	scan := func(labID string) response {
		return harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{
			"labId": labID, "type": "scan", "sentAt": "2026-10-06", "scanOrders": []map[string]any{{"scanner": "frontier"}},
		})
	}
	if refused := harness.expect(scan(labB), http.StatusConflict); refused.Body["code"] != "negatives_at_lab" {
		test.Fatalf("job at lab B = %s", refused.Raw)
	}
	// At the same lab a new stay opens from the old job's date, and the old job joins it.
	harness.expect(scan(labA), http.StatusCreated)
	roll := harness.rollOf(rollID)
	if roll["openHandoffId"] == nil || harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1 AND handoff_id IS NULL`, rollID) != 0 ||
		harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1 AND handed_at = DATE '2026-10-01'`, rollID) != 1 {
		test.Fatalf("after a job at the lab that holds the roll = %v", roll)
	}

	// One return through the old job brings the whole roll back.
	harness.expect(harness.call("PUT", "/processing/"+oldJob+"/negatives-returned", map[string]any{"date": "2026-10-10"}), http.StatusOK)
	if roll := harness.rollOf(rollID); roll["negativesAtLab"] != false || roll["openHandoffId"] != nil {
		test.Fatalf("after one return = %v", roll)
	}
	if harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1 AND negatives_returned_at = DATE '2026-10-10'`, rollID) != 2 {
		test.Fatal("a job of the roll has no return date")
	}
}

// A return through a job and through its handoff at the same time: both succeed, none deadlocks.
func TestConcurrentReturnsOfOneHandoff(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")
	for round := range 10 {
		rollID := harness.finishedRoll(seeded)
		jobID := newID()
		harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
		handoffID := harness.rollOf(rollID)["openHandoffId"].(string)
		date := map[string]any{"date": "2026-10-10"}
		statuses := race(2, func(index int) response {
			if index == 0 {
				return harness.call("PUT", "/processing/"+jobID+"/negatives-returned", date)
			}
			return harness.call("PUT", "/lab-handoffs/"+handoffID+"/returned", date)
		})
		if statuses[http.StatusOK] != 2 {
			test.Fatalf("round %d: statuses = %v", round, statuses)
		}
	}
}

// rollDevelopedAtLab: lab A developed the roll (developed_at) and still holds it; raw status at_lab.
func (harness *harness) rollDevelopedAtLab(seeded gear, labID string) (rollID, jobID string) {
	harness.test.Helper()
	rollID, jobID = harness.finishedRoll(seeded), newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	harness.execSQL(`UPDATE processing SET developed_at = DATE '2026-10-05' WHERE id = $1`, jobID)
	return rollID, jobID
}

// D8.3 in a batch: the lab that developed the roll and holds it scans it next, in the same stay.
func TestHandOverScanAtTheLabThatHoldsTheRoll(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA := harness.newLab("Lab A")
	rollID, _ := harness.rollDevelopedAtLab(seeded, labA)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/plan", map[string]any{"target": "scanned", "scanBy": "lab"}), http.StatusOK)
	handoffID := harness.rollOf(rollID)["openHandoffId"]

	body := handOver(labA, handed(rollID, "scan"))
	body["scanOrders"] = []map[string]any{{"scanner": "noritsu"}}
	harness.expect(harness.call("POST", "/lab-handoffs", body), http.StatusCreated)
	roll := harness.rollOf(rollID)
	if roll["openHandoffId"] != handoffID || roll["stageDetail"] != "lab_scanning" || roll["status"] != "at_lab" {
		test.Fatalf("after a scan at the same lab = %v", roll)
	}
	if harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, rollID) != 1 || harness.count(`SELECT count(*) FROM processing WHERE handoff_id = $1`, handoffID) != 2 {
		test.Fatal("the scan did not join the open handoff")
	}
}

// D8.1, D8.2, D8.3 for a single job.
func TestSingleJobFollowsTheLabThatHoldsTheRoll(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA, labB := harness.newLab("Lab A"), harness.newLab("Lab B")
	job := func(rollID string, labID any, jobType string) response {
		body := map[string]any{"type": jobType, "sentAt": "2026-10-06"}
		if labID != nil {
			body["labId"] = labID
		}
		if jobType != "develop" {
			body["scanOrders"] = []map[string]any{{"scanner": "frontier"}}
		}
		return harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), body)
	}

	// Scanned, negatives still at lab A: home and lab B refused, lab A accepted in the same stay.
	scanned := harness.finishedRoll(seeded)
	firstJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+scanned+"/processing/"+firstJob, map[string]any{
		"labId": labA, "type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+firstJob+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	for name, labID := range map[string]any{"home": nil, "lab B": labB} {
		if refused := harness.expect(job(scanned, labID, "scan"), http.StatusConflict); refused.Body["code"] != "negatives_at_lab" {
			test.Fatalf("%s scan = %s", name, refused.Raw)
		}
	}
	harness.expect(job(scanned, labA, "scan"), http.StatusCreated)
	if harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, scanned) != 1 {
		test.Fatal("the lab A scan opened a second handoff")
	}

	// Raw at_lab: lab A scans next only once its develop job is developed.
	atLab, developJob := harness.rollDevelopedAtLab(seeded, labA)
	harness.execSQL(`UPDATE processing SET developed_at = NULL WHERE id = $1`, developJob)
	if refused := harness.expect(job(atLab, labA, "scan"), http.StatusConflict); refused.Body["code"] != "roll_not_ready" {
		test.Fatalf("scan before developed = %s", refused.Raw)
	}
	harness.execSQL(`UPDATE processing SET developed_at = DATE '2026-10-05' WHERE id = $1`, developJob)
	harness.expect(job(atLab, labA, "scan"), http.StatusCreated)
	if roll := harness.rollOf(atLab); roll["status"] != "at_lab" || harness.count(`SELECT count(*) FROM lab_handoff WHERE roll_id = $1`, atLab) != 1 {
		test.Fatalf("after the lab A scan = %v", roll)
	}
}

// D8.6: a job handed to a lab keeps its lab; other fields still change.
func TestJobInAHandoffKeepsItsLab(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA, labB := harness.newLab("Lab A"), harness.newLab("Lab B")
	rollID, jobID := harness.rollDevelopedAtLab(seeded, labA)
	edit := func(labID any) response {
		body := map[string]any{"type": "develop", "sentAt": "2026-10-01", "price": 90000, "notes": "push +1"}
		if labID != nil {
			body["labId"] = labID
		}
		return harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, body)
	}
	for name, labID := range map[string]any{"lab B": labB, "home": nil} {
		if refused := harness.expect(edit(labID), http.StatusConflict); refused.Body["code"] != "lab_fixed_by_handoff" {
			test.Fatalf("move to %s = %s", name, refused.Raw)
		}
	}
	edited := harness.expect(edit(labA), http.StatusOK)
	if edited.Body["price"] != float64(90000) || edited.Body["notes"] != "push +1" {
		test.Fatalf("edit at the same lab = %s", edited.Raw)
	}
}

// D8.6 for an old lab job with no handoff: its lab_id is what says the lab holds the roll.
func TestOldLabJobKeepsItsLab(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labA := harness.newLab("Lab A")
	rollID := harness.finishedRoll(seeded)
	oldJob := newID()
	scanOrders := []map[string]any{{"scanner": "noritsu"}}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+oldJob, map[string]any{
		"labId": labA, "type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": scanOrders,
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+oldJob+"/scans-received", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	harness.execSQL(`UPDATE processing SET handoff_id = NULL WHERE id = $1`, oldJob)
	harness.execSQL(`DELETE FROM lab_handoff WHERE roll_id = $1`, rollID)

	moved := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+oldJob, map[string]any{
		"type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": scanOrders,
	}), http.StatusConflict)
	if moved.Body["code"] != "lab_fixed_by_handoff" {
		test.Fatalf("move home = %s", moved.Raw)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+oldJob, map[string]any{
		"labId": labA, "type": "develop_scan", "sentAt": "2026-10-01", "price": 120000, "notes": "hi-res", "scanOrders": scanOrders,
	}), http.StatusOK)

	if roll := harness.rollOf(rollID); roll["negativesAtLab"] != true {
		test.Fatalf("after the edits = %v", roll)
	}
	homeScan := harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{
		"type": "scan", "sentAt": "2026-10-06", "scanOrders": []map[string]any{{"scanner": "frontier"}},
	}), http.StatusConflict)
	if homeScan.Body["code"] != "negatives_at_lab" {
		test.Fatalf("home scan = %s", homeScan.Raw)
	}
}
