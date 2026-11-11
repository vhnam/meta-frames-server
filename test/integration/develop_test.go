package integration_test

import (
	"net/http"
	"testing"
)

// markDeveloped sends "Đã tráng"; a nil body sends no payload.
func (harness *harness) markDeveloped(rollID string, body map[string]any) response {
	if body == nil {
		return harness.call("PUT", "/rolls/"+rollID+"/developed", nil)
	}
	return harness.call("PUT", "/rolls/"+rollID+"/developed", body)
}

// finishedOctFirst is a finished roll with no job, finished on 2026-10-01 so the October dates below fit.
func (harness *harness) finishedOctFirst(seeded gear) string {
	rollID := harness.finishedRoll(seeded)
	harness.execSQL(`UPDATE roll SET finished_at = DATE '2026-10-01' WHERE id = $1`, rollID)
	return rollID
}

func detailRoll(result response) map[string]any { return asObject(result.Body["roll"]) }

// D4 on a job: a lab job gets only the developed date; a home develop job also closes.
func TestDevelopedDateGoesOnTheOpenDevelopJob(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")

	for _, jobType := range []string{"develop", "develop_scan"} {
		rollID, jobID := harness.finishedRoll(seeded), newID()
		body := map[string]any{"labId": labID, "type": jobType, "sentAt": "2026-10-01"}
		if jobType == "develop_scan" {
			body["scanOrders"] = []map[string]any{{"scanner": "noritsu"}}
		}
		harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, body), http.StatusCreated)
		harness.expect(harness.markDeveloped(rollID, map[string]any{"date": "2026-10-04"}), http.StatusOK)
		if harness.count(`SELECT count(*) FROM processing WHERE id = $1 AND developed_at = DATE '2026-10-04' AND negatives_returned_at IS NULL`, jobID) != 1 {
			test.Fatalf("lab %s job: dates not as expected", jobType)
		}
		if roll := harness.rollOf(rollID); roll["status"] != "at_lab" || roll["negativesAtLab"] != true {
			test.Fatalf("lab %s roll = %v", jobType, roll)
		}
		if job := harness.expect(harness.call("GET", "/processing/"+jobID, nil), http.StatusOK); job.Body["developedAt"] != "2026-10-04" {
			test.Fatalf("lab %s job read = %s", jobType, job.Raw)
		}
	}

	homeRoll, homeJob := harness.finishedRoll(seeded), newID()
	harness.expect(harness.call("PUT", "/rolls/"+homeRoll+"/processing/"+homeJob, map[string]any{"type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	if early := harness.expect(harness.markDeveloped(homeRoll, map[string]any{"date": "2026-09-30"}), http.StatusUnprocessableEntity); early.Body["code"] != "developed_before_sent" {
		test.Fatalf("early date = %s", early.Raw)
	}
	roll := detailRoll(harness.expect(harness.markDeveloped(homeRoll, map[string]any{"date": "2026-10-03"}), http.StatusOK))
	if roll["status"] != "developed" || roll["stage"] != "completed" {
		test.Fatalf("home roll = %v", roll)
	}
	if harness.count(`SELECT count(*) FROM processing WHERE id = $1 AND developed_at = DATE '2026-10-03' AND negatives_returned_at = DATE '2026-10-03'`, homeJob) != 1 {
		test.Fatal("home job: both dates should be the developed date")
	}
	// nothing left to develop
	if again := harness.expect(harness.markDeveloped(homeRoll, nil), http.StatusConflict); again.Body["code"] != "nothing_to_develop" {
		test.Fatalf("second Đã tráng = %s", again.Raw)
	}

	// a home develop_scan job gets both dates but stays open until its scans arrive
	homeScanRoll, homeScanJob := harness.finishedRoll(seeded), newID()
	harness.expect(harness.call("PUT", "/rolls/"+homeScanRoll+"/processing/"+homeScanJob, map[string]any{
		"type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	roll = detailRoll(harness.expect(harness.markDeveloped(homeScanRoll, map[string]any{"date": "2026-10-03"}), http.StatusOK))
	if roll["status"] != "at_lab" || roll["stageDetail"] != "home_scanning" {
		test.Fatalf("home develop_scan roll = %v", roll)
	}
	if harness.count(`SELECT count(*) FROM processing WHERE id = $1 AND developed_at = DATE '2026-10-03' AND negatives_returned_at = DATE '2026-10-03'`, homeScanJob) != 1 {
		test.Fatal("home develop_scan job: both dates should be the developed date")
	}
}

// D4 with no job: the roll gets one home develop job, closed, and goes by the chosen target.
func TestDevelopedWithNoJobCreatesAClosedHomeJob(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)

	developOnly := harness.finishedOctFirst(seeded)
	harness.expect(harness.call("PUT", "/rolls/"+developOnly+"/plan", map[string]any{"target": "scanned", "scanBy": "lab"}), http.StatusOK)
	roll := detailRoll(harness.expect(harness.markDeveloped(developOnly, map[string]any{"date": "2026-10-03", "target": "developed"}), http.StatusOK))
	plan := asObject(roll["plan"])
	if roll["stage"] != "completed" || plan["target"] != "developed" || plan["scanBy"] != nil {
		test.Fatalf("develop only = %v", roll)
	}
	closedHomeJob := `SELECT count(*) FROM processing WHERE roll_id = $1 AND type = 'develop' AND lab_id IS NULL
		AND sent_at = DATE '2026-10-03' AND developed_at = DATE '2026-10-03' AND negatives_returned_at = DATE '2026-10-03'`
	if harness.count(closedHomeJob, developOnly) != 1 {
		test.Fatal("no closed home develop job")
	}

	toScan := harness.finishedOctFirst(seeded)
	roll = detailRoll(harness.expect(harness.markDeveloped(toScan, map[string]any{"date": "2026-10-03", "target": "scanned"}), http.StatusOK))
	if roll["stage"] != "processing" || roll["stageDetail"] != "waiting_scan" || harness.count(closedHomeJob, toScan) != 1 {
		test.Fatalf("to be scanned = %v", roll)
	}

	inStock := harness.addRolls(seeded, 1)[0]
	if refused := harness.expect(harness.markDeveloped(inStock, nil), http.StatusConflict); refused.Body["code"] != "nothing_to_develop" {
		test.Fatalf("in stock = %s", refused.Raw)
	}
}

// Đã tráng refuses a date outside the roll's history, and saves nothing.
func TestDevelopedDateMustFitTheRollHistory(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")

	noJob := harness.finishedRoll(seeded)
	harness.execSQL(`UPDATE roll SET finished_at = DATE '2026-09-10' WHERE id = $1`, noJob)
	if refused := harness.expect(harness.markDeveloped(noJob, map[string]any{"date": "2026-09-05"}), http.StatusUnprocessableEntity); refused.Body["code"] != "developed_before_finished" {
		test.Fatalf("before finished = %s", refused.Raw)
	}
	if harness.count(`SELECT count(*) FROM processing WHERE roll_id = $1`, noJob) != 0 {
		test.Fatal("a refused Đã tráng created a job")
	}

	rollID, jobID := harness.finishedRoll(seeded), newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{
		"labId": labID, "type": "develop_scan", "sentAt": "2026-10-01", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	harness.expect(harness.call("PUT", "/processing/"+jobID+"/negatives-returned", map[string]any{"date": "2026-10-05"}), http.StatusOK)
	if refused := harness.expect(harness.markDeveloped(rollID, map[string]any{"date": "2026-10-08"}), http.StatusUnprocessableEntity); refused.Body["code"] != "developed_after_negatives_returned" {
		test.Fatalf("after negatives returned = %s", refused.Raw)
	}
	if harness.count(`SELECT count(*) FROM processing WHERE id = $1 AND developed_at IS NULL`, jobID) != 1 {
		test.Fatal("a refused Đã tráng saved a date")
	}
	harness.expect(harness.markDeveloped(rollID, map[string]any{"date": "2026-10-05"}), http.StatusOK)
}

// The other order: negatives cannot come back before the developed date, on a handoff or an old job.
func TestReturnDateMustNotPrecedeTheDevelopedDate(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	labID := harness.newLab("Lab A")

	rollID, jobID := harness.finishedOctFirst(seeded), newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+jobID, map[string]any{"labId": labID, "type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	harness.expect(harness.markDeveloped(rollID, map[string]any{"date": "2026-10-08"}), http.StatusOK)
	handoffID, _ := harness.rollOf(rollID)["openHandoffId"].(string)
	for _, path := range []string{"/processing/" + jobID + "/negatives-returned", "/lab-handoffs/" + handoffID + "/returned"} {
		if refused := harness.expect(harness.call("PUT", path, map[string]any{"date": "2026-10-05"}), http.StatusUnprocessableEntity); refused.Body["code"] != "returned_before_developed" {
			test.Fatalf("%s = %s", path, refused.Raw)
		}
	}
	if roll := harness.rollOf(rollID); roll["negativesAtLab"] != true {
		test.Fatalf("a refused return closed the handoff: %v", roll)
	}
	harness.expect(harness.call("PUT", "/lab-handoffs/"+handoffID+"/returned", map[string]any{"date": "2026-10-08"}), http.StatusOK)

	// an old lab job with no handoff
	oldRoll, oldJob := harness.finishedOctFirst(seeded), newID()
	harness.expect(harness.call("PUT", "/rolls/"+oldRoll+"/processing/"+oldJob, map[string]any{"labId": labID, "type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	harness.execSQL(`UPDATE processing SET handoff_id = NULL, developed_at = DATE '2026-10-08' WHERE id = $1`, oldJob)
	if refused := harness.expect(harness.call("PUT", "/processing/"+oldJob+"/negatives-returned", map[string]any{"date": "2026-10-05"}), http.StatusUnprocessableEntity); refused.Body["code"] != "returned_before_developed" {
		test.Fatalf("old job = %s", refused.Raw)
	}
	if harness.count(`SELECT count(*) FROM processing WHERE id = $1 AND negatives_returned_at IS NULL`, oldJob) != 1 {
		test.Fatal("a refused return saved a date on the old job")
	}
}

// D3: mark and unmark "Cần scan"; a single scan job removes the mark.
func TestScanRequestMarkAndASingleScanJob(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.finishedOctFirst(seeded)
	harness.expect(harness.markDeveloped(rollID, map[string]any{"date": "2026-10-03"}), http.StatusOK)

	if roll := detailRoll(harness.expect(harness.call("PUT", "/rolls/"+rollID+"/scan-request", nil), http.StatusOK)); roll["needsScan"] != true {
		test.Fatalf("marked = %v", roll)
	}
	if roll := detailRoll(harness.expect(harness.call("DELETE", "/rolls/"+rollID+"/scan-request", nil), http.StatusOK)); roll["needsScan"] != false {
		test.Fatalf("unmarked = %v", roll)
	}
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/scan-request", nil), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{
		"type": "scan", "sentAt": "2026-10-04", "scanOrders": []map[string]any{{"scanner": "noritsu"}},
	}), http.StatusCreated)
	if roll := harness.rollOf(rollID); roll["needsScan"] != false {
		test.Fatalf("after a scan job = %v", roll)
	}
}

// End to end through the job API: develop at home, Đã tráng, scan at home, done.
func TestHomeDevelopThenHomeScan(test *testing.T) {
	harness := newHarness(test)
	seeded := seedGear(harness)
	rollID := harness.finishedRoll(seeded)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/plan", map[string]any{"developBy": "self", "scanBy": "self", "target": "scanned"}), http.StatusOK)
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+newID(), map[string]any{"type": "develop", "sentAt": "2026-10-01"}), http.StatusCreated)
	if roll := harness.rollOf(rollID); roll["stageDetail"] != "home_developing" {
		test.Fatalf("developing = %v", roll)
	}

	roll := detailRoll(harness.expect(harness.markDeveloped(rollID, map[string]any{"date": "2026-10-02", "target": "scanned"}), http.StatusOK))
	if roll["stageDetail"] != "waiting_home_scan" || roll["status"] != "developed" {
		test.Fatalf("developed = %v", roll)
	}

	scanJob := newID()
	harness.expect(harness.call("PUT", "/rolls/"+rollID+"/processing/"+scanJob, map[string]any{
		"type": "scan", "sentAt": "2026-10-03", "scanOrders": []map[string]any{{"scanner": "other"}},
	}), http.StatusCreated)
	if roll := harness.rollOf(rollID); roll["stageDetail"] != "home_scanning" {
		test.Fatalf("scanning = %v", roll)
	}
	harness.expect(harness.call("PUT", "/processing/"+scanJob+"/scans-received", map[string]any{"date": "2026-10-04"}), http.StatusOK)
	if roll := harness.rollOf(rollID); roll["stage"] != "completed" || roll["status"] != "scanned" {
		test.Fatalf("done = %v", roll)
	}
}
