package processing

import (
	"context"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/testutil"
	"testing"
	"time"

	"github.com/google/uuid"
)

type processingFixture struct {
	queries *memoryQueries
	service *Service
	rollID  uuid.UUID
	labID   uuid.UUID
}

var noritsuOrder = []ScanOrderInput{{Scanner: domain.ScannerNoritsu}}

func newProcessingFixture() processingFixture {
	queries := newMemoryQueries()
	stockID, rollID, labID := uuid.New(), uuid.New(), uuid.New()
	queries.Stocks[stockID] = gen.FilmStock{ID: stockID, Process: "C-41"}
	queries.Rolls[rollID] = gen.Roll{ID: rollID, FilmStockID: stockID, Status: domain.RollStatusDoneShooting}
	queries.Labs[labID] = gen.Lab{ID: labID, Name: "Lab A"}
	return processingFixture{queries: queries, service: New(testutil.Store{Querier: queries}, fixedClock()), rollID: rollID, labID: labID}
}

func TestProcessingSaveSendsTheRollAndUpdatesTheJob(test *testing.T) {
	fixture := newProcessingFixture()
	ctx := context.Background()
	jobID := uuid.New()

	view, created, err := fixture.service.Save(ctx, fixture.rollID, jobID, Input{LabID: &fixture.labID, Type: domain.JobTypeDevelopScan, Notes: pointers.To(" rush "), ScanOrders: noritsuOrder})
	if err != nil || !created || view.Job.Process != "C-41" || !view.Job.SentAt.Equal(day(2026, time.October, 2)) {
		test.Fatalf("view=%+v created=%v err=%v", view, created, err)
	}
	if fixture.queries.Rolls[fixture.rollID].Status != domain.RollStatusAtLab {
		test.Fatalf("roll status = %q", fixture.queries.Rolls[fixture.rollID].Status)
	}

	view, created, err = fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelopScan, Price: pointers.To(90), ScanOrders: noritsuOrder})
	if err != nil || created || view.Job.Price == nil || *view.Job.Price != 90 {
		test.Fatalf("update: view=%+v created=%v err=%v", view, created, err)
	}
	_, _, err = fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelop})
	assertAppError(test, err, apperror.KindConflict, "job_immutable")
}

func TestProcessingSaveUsesTheGivenProcessAndRejectsUnknownLabsAndRolls(test *testing.T) {
	fixture := newProcessingFixture()
	ctx := context.Background()

	view, _, err := fixture.service.Save(ctx, fixture.rollID, uuid.New(), Input{Type: domain.JobTypeDevelopScan, Process: pointers.To("E-6"), ScanOrders: noritsuOrder})
	if err != nil || view.Job.Process != "E-6" {
		test.Fatalf("view=%+v err=%v", view, err)
	}
	_, _, err = fixture.service.Save(ctx, fixture.rollID, uuid.New(), Input{LabID: pointers.To(uuid.New()), Type: domain.JobTypeDevelopScan})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_lab")
	_, _, err = fixture.service.Save(ctx, uuid.New(), uuid.New(), Input{Type: domain.JobTypeDevelopScan})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestProcessingListGetAndNegativesAtLab(test *testing.T) {
	fixture := newProcessingFixture()
	ctx := context.Background()
	jobID := uuid.New()
	if _, _, err := fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelopScan, ScanOrders: noritsuOrder}); err != nil {
		test.Fatal(err)
	}

	views, err := fixture.service.ListForRoll(ctx, fixture.rollID)
	if err != nil || len(views) != 1 {
		test.Fatalf("views=%v err=%v", views, err)
	}
	if _, err := fixture.service.ListForRoll(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown roll must fail")
	}
	if view, err := fixture.service.Get(ctx, jobID); err != nil || view.Job.ID != jobID {
		test.Fatalf("view=%+v err=%v", view, err)
	}
	fixture.queries.AtLabRows = []gen.NegativesAtLabRow{{ID: jobID, LabName: "Lab A", DaysSinceSent: 3}}
	if rows, err := fixture.service.NegativesAtLab(ctx); err != nil || len(rows) != 1 {
		test.Fatalf("rows=%v err=%v", rows, err)
	}
}

// ---- film stocks ----

func TestRefreshRollStatusFollowsTheLifecycle(test *testing.T) {
	cases := []struct {
		name             string
		startStatus      string
		hasOpenJob       bool
		hasReceivedScans bool
		wantStatus       string
	}{
		{"open job keeps the roll at the lab", domain.RollStatusScanned, true, true, domain.RollStatusAtLab},
		{"closed job with scans", domain.RollStatusAtLab, false, true, domain.RollStatusScanned},
		{"closed job without scans", domain.RollStatusAtLab, false, false, domain.RollStatusDeveloped},
		{"statuses outside the lab flow are untouched", domain.RollStatusInCamera, false, true, domain.RollStatusInCamera},
		{"in stock is untouched", domain.RollStatusInStock, true, false, domain.RollStatusInStock},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			queries := newMemoryQueries()
			rollID := uuid.New()
			queries.Rolls[rollID] = gen.Roll{ID: rollID, Status: testCase.startStatus}
			queries.HasOpenJob, queries.HasReceivedScans = testCase.hasOpenJob, testCase.hasReceivedScans

			if err := RefreshRollStatus(context.Background(), queries, rollID); err != nil {
				test.Fatal(err)
			}
			if got := queries.Rolls[rollID].Status; got != testCase.wantStatus {
				test.Fatalf("status = %s, want %s", got, testCase.wantStatus)
			}
		})
	}
}

func jobFixture(queries *memoryQueries, jobType string, labID *uuid.UUID) (rollID, jobID uuid.UUID) {
	rollID, jobID = uuid.New(), uuid.New()
	queries.Rolls[rollID] = gen.Roll{ID: rollID, Status: domain.RollStatusAtLab}
	queries.Jobs[jobID] = gen.Processing{ID: jobID, RollID: rollID, LabID: labID, Type: jobType, SentAt: day(2026, time.September, 1)}
	return rollID, jobID
}

func TestRecordNegativesReturnedIsNotNeededForHomeProcessing(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	_, jobID := jobFixture(queries, domain.JobTypeDevelop, nil)

	_, err := service.RecordNegativesReturned(context.Background(), jobID, nil)
	assertAppError(test, err, apperror.KindConflict, "home_processing")
}

func TestRecordNegativesReturnedDefaultsToTodayAndClosesDevelopOnlyJobs(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	labID := uuid.New()
	rollID, jobID := jobFixture(queries, domain.JobTypeDevelop, &labID)

	view, err := service.RecordNegativesReturned(context.Background(), jobID, nil)
	if err != nil {
		test.Fatal(err)
	}
	if view.Job.NegativesReturnedAt == nil || !view.Job.NegativesReturnedAt.Equal(day(2026, time.October, 2)) {
		test.Fatalf("NegativesReturnedAt = %v, want today", view.Job.NegativesReturnedAt)
	}
	if view.IsOpen {
		test.Fatal("a develop-only job is closed once negatives are back")
	}
	if got := queries.Rolls[rollID].Status; got != domain.RollStatusDeveloped {
		test.Fatalf("roll status = %s, want developed", got)
	}
}

func TestRecordScansReceivedRejectsJobsWithoutScans(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	labID := uuid.New()
	_, jobID := jobFixture(queries, domain.JobTypePrint, &labID)

	_, err := service.RecordScansReceived(context.Background(), jobID, nil)
	assertAppError(test, err, apperror.KindConflict, "no_scans_expected")
}

func TestRecordScansReceivedUsesTheGivenDateAndScansTheRoll(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	labID := uuid.New()
	rollID, jobID := jobFixture(queries, domain.JobTypeDevelopScan, &labID)
	queries.HasReceivedScans = true // the freshly recorded date counts as received scans
	given := day(2026, time.September, 20)

	view, err := service.RecordScansReceived(context.Background(), jobID, &given)
	if err != nil {
		test.Fatal(err)
	}
	if !view.Job.ScansReceivedAt.Equal(given) {
		test.Fatalf("ScansReceivedAt = %v", view.Job.ScansReceivedAt)
	}
	if got := queries.Rolls[rollID].Status; got != domain.RollStatusScanned {
		test.Fatalf("roll status = %s, want scanned", got)
	}
}

func TestProcessingUnknownJobIsNotFound(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()}, fixedClock())
	_, err := service.RecordScansReceived(context.Background(), uuid.New(), nil)
	assertAppError(test, err, apperror.KindNotFound, "not_found")
	_, err = service.Get(context.Background(), uuid.New())
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestIsJobOpen(test *testing.T) {
	received := day(2026, time.January, 1)
	cases := []struct {
		name string
		job  gen.Processing
		want bool
	}{
		{"develop_scan awaiting scans", gen.Processing{Type: domain.JobTypeDevelopScan}, true},
		{"develop_scan with scans", gen.Processing{Type: domain.JobTypeDevelopScan, ScansReceivedAt: &received}, false},
		{"develop_scan negatives alone do not close", gen.Processing{Type: domain.JobTypeDevelopScan, NegativesReturnedAt: &received}, true},
		{"scan awaiting scans", gen.Processing{Type: domain.JobTypeScan}, true},
		{"develop awaiting negatives", gen.Processing{Type: domain.JobTypeDevelop}, true},
		{"develop with negatives", gen.Processing{Type: domain.JobTypeDevelop, NegativesReturnedAt: &received}, false},
		{"print awaiting negatives", gen.Processing{Type: domain.JobTypePrint}, true},
	}
	for _, testCase := range cases {
		if got := isJobOpen(testCase.job); got != testCase.want {
			test.Errorf("%s: isJobOpen = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestStatusAfterJobs(test *testing.T) {
	cases := []struct {
		hasOpenJob, hasReceivedScans bool
		want                         string
	}{
		{true, false, domain.RollStatusAtLab},
		{true, true, domain.RollStatusAtLab},
		{false, true, domain.RollStatusScanned},
		{false, false, domain.RollStatusDeveloped},
	}
	for _, testCase := range cases {
		if got := statusAfterJobs(testCase.hasOpenJob, testCase.hasReceivedScans); got != testCase.want {
			test.Errorf("statusAfterJobs(%v, %v) = %s, want %s", testCase.hasOpenJob, testCase.hasReceivedScans, got, testCase.want)
		}
	}
}

func TestCheckCanSend(test *testing.T) {
	cases := []struct {
		rollStatus, jobType string
		wantCode            string
	}{
		{domain.RollStatusDoneShooting, domain.JobTypeDevelopScan, ""},
		{domain.RollStatusDoneShooting, domain.JobTypeDevelop, ""},
		{domain.RollStatusScanned, domain.JobTypeScan, ""},
		{domain.RollStatusDeveloped, domain.JobTypePrint, ""},
		{domain.RollStatusScanned, domain.JobTypeDevelop, "invalid_job_type"},
		{domain.RollStatusDeveloped, domain.JobTypeDevelopScan, "invalid_job_type"},
		{domain.RollStatusInStock, domain.JobTypeScan, "roll_not_ready"},
		{domain.RollStatusInCamera, domain.JobTypeDevelop, "roll_not_ready"},
		{domain.RollStatusAtLab, domain.JobTypeScan, "roll_not_ready"},
	}
	for _, testCase := range cases {
		err := checkCanSend(testCase.rollStatus, testCase.jobType)
		if testCase.wantCode == "" {
			if err != nil {
				test.Errorf("%s/%s unexpectedly rejected: %v", testCase.rollStatus, testCase.jobType, err)
			}
			continue
		}
		assertAppError(test, err, apperror.KindConflict, testCase.wantCode)
	}
}

func TestDeleteRemovesTheJobAndRevertsTheRollWhenItWasTheLastOne(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	rollID, jobID := jobFixture(queries, domain.JobTypeDevelopScan, nil)

	if err := service.Delete(context.Background(), jobID); err != nil {
		test.Fatal(err)
	}
	if !queries.Jobs[jobID].DeletedAt.Valid {
		test.Fatal("the job row must be kept with deleted_at set")
	}
	if got := queries.Rolls[rollID].Status; got != domain.RollStatusDoneShooting {
		test.Fatalf("a roll without jobs is back to done_shooting, got %q", got)
	}
	if _, err := service.Get(context.Background(), jobID); err == nil {
		test.Fatal("a deleted job must be invisible")
	}
}

func TestDeleteRecomputesTheRollStatusWhenOtherJobsRemain(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	rollID, openJob := jobFixture(queries, domain.JobTypeDevelopScan, nil)
	receivedAt := day(2026, time.September, 20)
	doneJob := uuid.New()
	queries.Jobs[doneJob] = gen.Processing{ID: doneJob, RollID: rollID, Type: domain.JobTypeDevelopScan, SentAt: day(2026, time.September, 2), ScansReceivedAt: &receivedAt}
	queries.HasReceivedScans = true

	if err := service.Delete(context.Background(), openJob); err != nil {
		test.Fatal(err)
	}
	if got := queries.Rolls[rollID].Status; got != domain.RollStatusScanned {
		test.Fatalf("with only a finished job left the roll is scanned, got %q", got)
	}
}

func TestDeleteRefusesJobsWithScansAndUnknownJobs(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	_, jobID := jobFixture(queries, domain.JobTypeDevelopScan, nil)
	scanID := uuid.New()
	queries.Scans[scanID] = gen.Scan{ID: scanID, ProcessingID: jobID}

	assertAppError(test, service.Delete(context.Background(), jobID), apperror.KindConflict, "job_has_scans")
	assertAppError(test, service.Delete(context.Background(), uuid.New()), apperror.KindNotFound, "not_found")
}

func TestProcessingSaveValidatesScanOrders(test *testing.T) {
	tests := []struct {
		name  string
		input Input
		kind  apperror.Kind
		code  string
	}{
		{"scan job needs orders", Input{Type: domain.JobTypeScan}, apperror.KindUnprocessable, "scan_orders_required"},
		{"develop job takes none", Input{Type: domain.JobTypeDevelop, ScanOrders: noritsuOrder}, apperror.KindUnprocessable, "scan_not_applicable"},
		{"duplicate scanner", Input{Type: domain.JobTypeDevelopScan, ScanOrders: []ScanOrderInput{{Scanner: domain.ScannerNoritsu}, {Scanner: domain.ScannerNoritsu, HiRes: true}}}, apperror.KindUnprocessable, "duplicate_scanner"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			fixture := newProcessingFixture()
			_, _, err := fixture.service.Save(context.Background(), fixture.rollID, uuid.New(), testCase.input)
			assertAppError(test, err, testCase.kind, testCase.code)
		})
	}
}

func TestProcessingSaveReplacesScanOrders(test *testing.T) {
	fixture := newProcessingFixture()
	ctx := context.Background()
	jobID := uuid.New()
	both := []ScanOrderInput{{Scanner: domain.ScannerNoritsu, HiRes: true}, {Scanner: domain.ScannerFrontier}}

	view, _, err := fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelopScan, ScanOrders: both})
	if err != nil || len(view.ScanOrders) != 2 || view.ScanOrders[0].Scanner != domain.ScannerFrontier || !view.ScanOrders[1].HiRes {
		test.Fatalf("view = %+v err=%v", view.ScanOrders, err)
	}

	view, _, err = fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelopScan, ScanOrders: noritsuOrder})
	if err != nil || len(view.ScanOrders) != 1 || view.ScanOrders[0].HiRes {
		test.Fatalf("after replace = %+v err=%v", view.ScanOrders, err)
	}
}

func TestProcessingSaveKeepsScannersThatHaveScans(test *testing.T) {
	fixture := newProcessingFixture()
	ctx := context.Background()
	jobID := uuid.New()
	both := []ScanOrderInput{{Scanner: domain.ScannerNoritsu}, {Scanner: domain.ScannerFrontier}}
	if _, _, err := fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelopScan, ScanOrders: both}); err != nil {
		test.Fatal(err)
	}
	scanID := uuid.New()
	fixture.queries.Scans[scanID] = gen.Scan{ID: scanID, ProcessingID: jobID, Scanner: domain.ScannerFrontier}

	_, _, err := fixture.service.Save(ctx, fixture.rollID, jobID, Input{Type: domain.JobTypeDevelopScan, ScanOrders: noritsuOrder})
	assertAppError(test, err, apperror.KindConflict, "scanner_has_scans")
}
