package roll

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

func TestExpiryEnd(test *testing.T) {
	cases := []struct {
		name        string
		year, month *int32
		want        *time.Time
	}{
		{"no expiry", nil, nil, nil},
		{"month known", int32Ref(2027), int32Ref(6), ptrTime(day(2027, time.June, 30))},
		{"february leap year", int32Ref(2028), int32Ref(2), ptrTime(day(2028, time.February, 29))},
		{"december", int32Ref(2026), int32Ref(12), ptrTime(day(2026, time.December, 31))},
		{"unknown month means december", int32Ref(2026), nil, ptrTime(day(2026, time.December, 31))},
	}
	for _, testCase := range cases {
		got := expiryEnd(testCase.year, testCase.month)
		switch {
		case got == nil && testCase.want == nil:
		case got == nil || testCase.want == nil || !got.Equal(*testCase.want):
			test.Errorf("%s: expiryEnd = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func TestIsExpiredIsInclusiveOfTheLastDay(test *testing.T) {
	year, month := int32Ref(2026), int32Ref(6)
	if isExpired(year, month, day(2026, time.June, 30)) {
		test.Fatal("a roll is still valid on the last day of its expiry month")
	}
	if !isExpired(year, month, day(2026, time.July, 1)) {
		test.Fatal("a roll is expired the day after its expiry month")
	}
	if isExpired(nil, nil, day(2030, time.January, 1)) {
		test.Fatal("a roll without expiry never expires")
	}
}

func rollExpiring(year, month *int32) Summary {
	return Summary{gen.ListRollSummariesRow{ExpiryYear: year, ExpiryMonth: month}}
}

func TestBuildExpiryReport(test *testing.T) {
	today := day(2026, time.October, 2)
	rolls := []Summary{
		rollExpiring(int32Ref(2027), int32Ref(3)), // 2027-03-31: inside the 6-month horizon
		rollExpiring(int32Ref(2027), int32Ref(4)), // 2027-04-30: beyond 2027-04-02
		rollExpiring(int32Ref(2020), nil),         // long expired
		rollExpiring(nil, nil),                    // no expiry
		rollExpiring(int32Ref(2026), int32Ref(10)),
	}
	report := buildExpiryReport(rolls, today)

	if len(report.NoExpiry) != 1 {
		test.Fatalf("NoExpiry = %d, want 1", len(report.NoExpiry))
	}
	if len(report.Expiring) != 3 {
		test.Fatalf("Expiring = %d, want 3", len(report.Expiring))
	}
	wantOrder := []time.Time{day(2020, time.December, 31), day(2026, time.October, 31), day(2027, time.March, 31)}
	wantExpired := []bool{true, false, false}
	for index, expiring := range report.Expiring {
		if !expiring.ExpiresOn.Equal(wantOrder[index]) || expiring.Expired != wantExpired[index] {
			test.Errorf("Expiring[%d] = %v expired=%v, want %v expired=%v", index, expiring.ExpiresOn, expiring.Expired, wantOrder[index], wantExpired[index])
		}
	}
}

func TestAddBulkCreatesRollsAndReplaysByIdempotencyKey(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	input := BulkInput{
		FilmStockID: fixture.queries.Rolls[fixture.rollID].FilmStockID, Format: 135, Quantity: 3,
		Price: pointers.To(900), IdempotencyKey: "key-1",
	}

	created, err := fixture.service.AddBulk(ctx, input)
	if err != nil || len(created) != 3 || created[0].Exposures != 36 || created[0].Status != domain.RollStatusInStock {
		test.Fatalf("created=%+v err=%v", created, err)
	}
	total := len(fixture.queries.Rolls)

	replayed, err := fixture.service.AddBulk(ctx, input)
	if err != nil || len(replayed) != 3 || len(fixture.queries.Rolls) != total {
		test.Fatalf("a retry must return the first attempt's rolls, got %d rolls (total %d) err=%v", len(replayed), len(fixture.queries.Rolls), err)
	}
}

func TestAddBulkWithoutKeyAlwaysCreates(test *testing.T) {
	fixture := newLoadFixture()
	input := BulkInput{FilmStockID: fixture.queries.Rolls[fixture.rollID].FilmStockID, Format: 135, Quantity: 2}
	before := len(fixture.queries.Rolls)
	if _, err := fixture.service.AddBulk(context.Background(), input); err != nil {
		test.Fatal(err)
	}
	if len(fixture.queries.Rolls) != before+2 {
		test.Fatalf("rolls = %d", len(fixture.queries.Rolls))
	}
}

func TestAddBulkRejectsUnknownStockAndInProgressKeys(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	_, err := fixture.service.AddBulk(ctx, BulkInput{FilmStockID: uuid.New(), Format: 135, Quantity: 1})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_film_stock")

	fixture.queries.Idempotency["busy"] = gen.IdempotencyKey{Key: "busy"}
	_, err = fixture.service.AddBulk(ctx, BulkInput{FilmStockID: uuid.New(), Format: 135, Quantity: 1, IdempotencyKey: "busy"})
	assertAppError(test, err, apperror.KindConflict, "request_in_progress")
}

func TestRollListAndDetail(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	fixture.queries.Spend = gen.RollSpendRow{RollPrice: 100, ProcessingPrice: 50, ProcessingPriceMissing: true}

	rolls, err := fixture.service.List(ctx, Filter{Status: pointers.To(domain.RollStatusInStock), Format: pointers.To(135)})
	if err != nil || len(rolls) != 1 {
		test.Fatalf("rolls=%v err=%v", rolls, err)
	}
	detail, err := fixture.service.Detail(ctx, fixture.rollID)
	if err != nil || detail.Totals.RollPrice != 100 || !detail.Totals.Incomplete || detail.Stock.BoxIso != 400 {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
	if _, err := fixture.service.Detail(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown roll must fail")
	}
}

func TestRollDetailWarnsAboutExpiredStockAndLoadsTheBaseStock(test *testing.T) {
	fixture := newLoadFixture()
	baseID := uuid.New()
	stock := fixture.queries.Stocks[fixture.queries.Rolls[fixture.rollID].FilmStockID]
	stock.BaseStockID = &baseID
	fixture.queries.Stocks[stock.ID] = stock
	fixture.queries.Stocks[baseID] = gen.FilmStock{ID: baseID, Brand: "Kodak"}
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.ExpiryYear = pointers.To(int32(2020))
	fixture.queries.Rolls[fixture.rollID] = roll

	detail, err := fixture.service.Detail(context.Background(), fixture.rollID)
	if err != nil || detail.BaseStock == nil || detail.BaseStock.Brand != "Kodak" || len(detail.Warnings) != 1 {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestRollUpdateChangesTheDetails(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	stockID := fixture.queries.Rolls[fixture.rollID].FilmStockID
	started := day(2026, time.September, 1)

	detail, err := fixture.service.Update(ctx, fixture.rollID, Input{
		FilmStockID: stockID, Format: 120, Exposures: 12, Price: pointers.To(800), StartedAt: &started, Description: pointers.To(" holiday "),
	})
	if err != nil || detail.Summary.Format != 120 || detail.Summary.Exposures != 12 {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
	if got := fixture.queries.Rolls[fixture.rollID]; got.Description == nil || *got.Description != "holiday" {
		test.Fatalf("roll = %+v", got)
	}
}

func TestRollUpdateGuardsStockChangesAndUnknownStocks(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	stockID := fixture.queries.Rolls[fixture.rollID].FilmStockID

	_, err := fixture.service.Update(ctx, fixture.rollID, Input{FilmStockID: uuid.New(), Format: 135, Exposures: 36})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_film_stock")

	otherID := uuid.New()
	fixture.queries.Stocks[otherID] = gen.FilmStock{ID: otherID}
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.Status = domain.RollStatusInCamera
	fixture.queries.Rolls[fixture.rollID] = roll
	_, err = fixture.service.Update(ctx, fixture.rollID, Input{FilmStockID: otherID, Format: 135, Exposures: 36})
	assertAppError(test, err, apperror.KindConflict, "stock_locked")

	_, err = fixture.service.Update(ctx, uuid.New(), Input{FilmStockID: stockID, Format: 135, Exposures: 36})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestLoadPutsTheRollInTheCameraAndIsRepeatable(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	lensID := uuid.New()
	fixture.queries.Lenses[lensID] = gen.Lens{ID: lensID, IsActive: true}
	lenses := []uuid.UUID{lensID}
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.ExpiryYear = pointers.To(int32(2020))
	fixture.queries.Rolls[fixture.rollID] = roll

	detail, err := fixture.service.Load(ctx, fixture.rollID, LoadInput{CameraID: fixture.cameraID, LensIDs: &lenses})
	if err != nil {
		test.Fatal(err)
	}
	if detail.Summary.Status != domain.RollStatusInCamera || detail.Summary.ShotIso != nil || len(detail.Lenses) != 1 || len(detail.Warnings) != 1 {
		test.Fatalf("detail = %+v", detail)
	}
	if !detail.Summary.StartedAt.Equal(day(2026, time.October, 2)) {
		test.Fatalf("StartedAt = %v, want today", detail.Summary.StartedAt)
	}
	again, err := fixture.service.Load(ctx, fixture.rollID, LoadInput{CameraID: fixture.cameraID})
	if err != nil || again.Summary.Status != domain.RollStatusInCamera || len(again.Warnings) != 0 {
		test.Fatalf("repeat load = %+v err=%v", again, err)
	}
}

func TestRollSetLensesReplacesTheLensesOnARoll(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	first, second, inactive := uuid.New(), uuid.New(), uuid.New()
	fixture.queries.Lenses[first] = gen.Lens{ID: first, IsActive: true}
	fixture.queries.Lenses[second] = gen.Lens{ID: second, IsActive: true}
	fixture.queries.Lenses[inactive] = gen.Lens{ID: inactive}
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.CameraID = &fixture.cameraID
	roll.Status = domain.RollStatusInCamera
	fixture.queries.Rolls[fixture.rollID] = roll

	lenses, err := fixture.service.SetLenses(ctx, fixture.rollID, []uuid.UUID{first, first})
	if err != nil || len(lenses) != 1 {
		test.Fatalf("lenses=%v err=%v", lenses, err)
	}
	lenses, err = fixture.service.SetLenses(ctx, fixture.rollID, []uuid.UUID{second})
	if err != nil || len(lenses) != 1 || lenses[0].ID != second {
		test.Fatalf("replacing must drop the old lens, got %v err=%v", lenses, err)
	}
	_, err = fixture.service.SetLenses(ctx, fixture.rollID, []uuid.UUID{inactive})
	assertAppError(test, err, apperror.KindUnprocessable, "inactive_lens")
	_, err = fixture.service.SetLenses(ctx, fixture.rollID, []uuid.UUID{uuid.New()})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_lens")

	camera := fixture.queries.Cameras[fixture.cameraID]
	camera.HasFixedLens = true
	fixture.queries.Cameras[fixture.cameraID] = camera
	_, err = fixture.service.SetLenses(ctx, fixture.rollID, []uuid.UUID{first})
	assertAppError(test, err, apperror.KindConflict, "fixed_lens_camera")
}

func TestFinishDefaultsToTodayAndUsesTheGivenDate(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.Status = domain.RollStatusInCamera
	fixture.queries.Rolls[fixture.rollID] = roll

	detail, err := fixture.service.Finish(ctx, fixture.rollID, nil)
	if err != nil || detail.Summary.Status != domain.RollStatusDoneShooting || !detail.Summary.FinishedAt.Equal(day(2026, time.October, 2)) {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
	roll.Status = domain.RollStatusInCamera
	fixture.queries.Rolls[fixture.rollID] = roll
	given := day(2026, time.August, 1)
	detail, err = fixture.service.Finish(ctx, fixture.rollID, &given)
	if err != nil || !detail.Summary.FinishedAt.Equal(given) {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func TestExpiryReportListsOnlyInStockRolls(test *testing.T) {
	fixture := newLoadFixture()
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.ExpiryYear, roll.ExpiryMonth = pointers.To(int32(2020)), pointers.To(int32(1))
	fixture.queries.Rolls[fixture.rollID] = roll
	otherID := uuid.New()
	fixture.queries.Rolls[otherID] = gen.Roll{ID: otherID, Status: domain.RollStatusInCamera, ExpiryYear: pointers.To(int32(2020))}

	report, err := fixture.service.ExpiryReport(context.Background())
	if err != nil || len(report.Expiring) != 1 || len(report.NoExpiry) != 0 {
		test.Fatalf("report=%+v err=%v", report, err)
	}
}

type loadFixture struct {
	queries  *memoryQueries
	service  *Service
	rollID   uuid.UUID
	cameraID uuid.UUID
}

func newLoadFixture() loadFixture {
	queries := newMemoryQueries()
	stockID, rollID, cameraID := uuid.New(), uuid.New(), uuid.New()
	queries.Stocks[stockID] = gen.FilmStock{ID: stockID, BoxIso: 400}
	queries.Rolls[rollID] = gen.Roll{ID: rollID, FilmStockID: stockID, Status: domain.RollStatusInStock}
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, IsActive: true, Mount: stringRef("F")}
	return loadFixture{queries: queries, service: New(testutil.Store{Querier: queries}, fixedClock()), rollID: rollID, cameraID: cameraID}
}

func TestLoadRejectsRollsThatAreNotInStock(test *testing.T) {
	fixture := newLoadFixture()
	roll := fixture.queries.Rolls[fixture.rollID]
	roll.Status = domain.RollStatusDoneShooting
	fixture.queries.Rolls[fixture.rollID] = roll

	_, err := fixture.service.Load(context.Background(), fixture.rollID, LoadInput{CameraID: fixture.cameraID})
	assertAppError(test, err, apperror.KindConflict, "roll_not_in_stock")
}

func TestLoadRejectsInactiveAndLoadedAndUnknownCameras(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()

	camera := fixture.queries.Cameras[fixture.cameraID]
	camera.IsActive = false
	fixture.queries.Cameras[fixture.cameraID] = camera
	_, err := fixture.service.Load(ctx, fixture.rollID, LoadInput{CameraID: fixture.cameraID})
	assertAppError(test, err, apperror.KindConflict, "camera_inactive")

	camera.IsActive = true
	fixture.queries.Cameras[fixture.cameraID] = camera
	fixture.queries.LoadedCameras[fixture.cameraID] = true
	_, err = fixture.service.Load(ctx, fixture.rollID, LoadInput{CameraID: fixture.cameraID})
	assertAppError(test, err, apperror.KindConflict, "camera_loaded")

	_, err = fixture.service.Load(ctx, fixture.rollID, LoadInput{CameraID: uuid.New()})
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_camera")

	_, err = fixture.service.Load(ctx, uuid.New(), LoadInput{CameraID: fixture.cameraID})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestLensesForLoadUsesTheBuiltInLensOfFixedLensCameras(test *testing.T) {
	queries := newMemoryQueries()
	cameraID, lensID := uuid.New(), uuid.New()
	camera := gen.Camera{ID: cameraID, HasFixedLens: true}
	queries.Cameras[cameraID] = camera
	ctx := context.Background()

	// A fixed-lens camera with no linked lens is a data error (UC-17 6c).
	_, err := lensesForLoad(ctx, queries, camera, nil)
	assertAppError(test, err, apperror.KindConflict, "fixed_lens_data_error")

	queries.Lenses[lensID] = gen.Lens{ID: lensID, IsBuiltIn: true, IsActive: true}
	queries.Links = []gen.CameraLens{{CameraID: cameraID, LensID: lensID}}
	ignored := []uuid.UUID{uuid.New()}
	lensIDs, err := lensesForLoad(ctx, queries, camera, &ignored)
	if err != nil || len(lensIDs) != 1 || lensIDs[0] != lensID {
		test.Fatalf("lensIDs=%v err=%v (requested lenses are ignored for fixed-lens cameras)", lensIDs, err)
	}
}

func TestLensesForLoadValidatesRequestedLenses(test *testing.T) {
	queries := newMemoryQueries()
	camera := gen.Camera{ID: uuid.New(), Mount: stringRef("F")}
	activeID, inactiveID := uuid.New(), uuid.New()
	queries.Lenses[activeID] = gen.Lens{ID: activeID, IsActive: true}
	queries.Lenses[inactiveID] = gen.Lens{ID: inactiveID}
	ctx := context.Background()

	lensIDs, err := lensesForLoad(ctx, queries, camera, nil)
	if err != nil || lensIDs != nil {
		test.Fatalf("skipping lenses: %v %v", lensIDs, err)
	}
	requested := []uuid.UUID{activeID, activeID}
	lensIDs, err = lensesForLoad(ctx, queries, camera, &requested)
	if err != nil || len(lensIDs) != 1 {
		test.Fatalf("lensIDs=%v err=%v (duplicates must collapse)", lensIDs, err)
	}
	unknown := []uuid.UUID{uuid.New()}
	_, err = lensesForLoad(ctx, queries, camera, &unknown)
	assertAppError(test, err, apperror.KindUnprocessable, "unknown_lens")
	inactive := []uuid.UUID{inactiveID}
	_, err = lensesForLoad(ctx, queries, camera, &inactive)
	assertAppError(test, err, apperror.KindUnprocessable, "inactive_lens")
}

func TestAddBulkValidatesBeforeTouchingTheDatabase(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()}, fixedClock())
	cases := []struct {
		name     string
		input    BulkInput
		wantCode string
	}{
		{"month without year", BulkInput{Format: 135, Quantity: 1, ExpiryMonth: intRef(3)}, "expiry_year_required"},
		{"bad month", BulkInput{Format: 135, Quantity: 1, ExpiryYear: intRef(2027), ExpiryMonth: intRef(13)}, "invalid_expiry_month"},
		{"120 needs exposures", BulkInput{Format: 120, Quantity: 1}, "exposures_required"},
	}
	for _, testCase := range cases {
		_, err := service.AddBulk(context.Background(), testCase.input)
		assertAppError(test, err, apperror.KindUnprocessable, testCase.wantCode)
	}
}

func TestRollUpdateValidatesDates(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()}, fixedClock())
	started, finished := day(2026, 5, 10), day(2026, 5, 1)
	_, err := service.Update(context.Background(), uuid.New(), Input{StartedAt: &started, FinishedAt: &finished})
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_dates")
}

func TestSetRollLensesRequiresALoadedInterchangeableCamera(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	ctx := context.Background()

	notLoadedID := uuid.New()
	queries.Rolls[notLoadedID] = gen.Roll{ID: notLoadedID, Status: domain.RollStatusInStock}
	_, err := service.SetLenses(ctx, notLoadedID, nil)
	assertAppError(test, err, apperror.KindConflict, "roll_not_loaded")

	cameraID, fixedRollID := uuid.New(), uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, HasFixedLens: true}
	queries.Rolls[fixedRollID] = gen.Roll{ID: fixedRollID, Status: domain.RollStatusInCamera, CameraID: &cameraID}
	_, err = service.SetLenses(ctx, fixedRollID, nil)
	assertAppError(test, err, apperror.KindConflict, "fixed_lens_camera")

	_, err = service.SetLenses(ctx, uuid.New(), nil)
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestFinishRequiresARollInACamera(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries}, fixedClock())
	rollID := uuid.New()
	queries.Rolls[rollID] = gen.Roll{ID: rollID, Status: domain.RollStatusInStock}

	_, err := service.Finish(context.Background(), rollID, nil)
	assertAppError(test, err, apperror.KindConflict, "roll_not_in_camera")
}

func TestCheckUsableLensesOnARoll(test *testing.T) {
	inactiveID := uuid.New()
	inactive := gen.Lens{ID: inactiveID}
	assertAppError(test, checkUsableLenses([]gen.Lens{inactive}, []uuid.UUID{inactiveID}, nil), apperror.KindUnprocessable, "inactive_lens")
	if err := checkUsableLenses([]gen.Lens{inactive}, []uuid.UUID{inactiveID}, map[uuid.UUID]bool{inactiveID: true}); err != nil {
		test.Fatalf("a lens already on the roll must stay: %v", err)
	}
	assertAppError(test, checkUsableLenses(nil, []uuid.UUID{inactiveID}, nil), apperror.KindUnprocessable, "unknown_lens")
}

func TestValidateExpiry(test *testing.T) {
	year, month, badMonth := intRef(2027), intRef(6), intRef(13)
	if err := validateExpiry(nil, nil); err != nil {
		test.Fatalf("no expiry rejected: %v", err)
	}
	if err := validateExpiry(year, nil); err != nil {
		test.Fatalf("year only rejected: %v", err)
	}
	if err := validateExpiry(year, month); err != nil {
		test.Fatalf("year and month rejected: %v", err)
	}
	assertAppError(test, validateExpiry(nil, month), apperror.KindUnprocessable, "expiry_year_required")
	assertAppError(test, validateExpiry(year, badMonth), apperror.KindUnprocessable, "invalid_expiry_month")
	assertAppError(test, validateExpiry(year, intRef(0)), apperror.KindUnprocessable, "invalid_expiry_month")
}

func intRef(value int) *int { return &value }

func TestResolveExposures(test *testing.T) {
	if got, err := resolveExposures(135, nil); err != nil || got != 36 {
		test.Fatalf("135 default = %d, %v", got, err)
	}
	if got, err := resolveExposures(135, intRef(24)); err != nil || got != 24 {
		test.Fatalf("explicit = %d, %v", got, err)
	}
	if got, err := resolveExposures(120, intRef(12)); err != nil || got != 12 {
		test.Fatalf("120 explicit = %d, %v", got, err)
	}
	_, err := resolveExposures(120, nil)
	assertAppError(test, err, apperror.KindUnprocessable, "exposures_required")
}

func TestNormalizeShotISO(test *testing.T) {
	if got := normalizeShotISO(nil, 400); got != nil {
		test.Fatalf("nil shot ISO = %v", got)
	}
	if got := normalizeShotISO(intRef(400), 400); got != nil {
		test.Fatalf("box ISO must become null, got %v", *got)
	}
	if got := normalizeShotISO(intRef(800), 400); got == nil || *got != 800 {
		test.Fatalf("pushed ISO = %v", got)
	}
}

func TestRollTotals(test *testing.T) {
	if got := (Totals{RollPrice: 150000, ProcessingPrice: 90000}).Total(); got != 240000 {
		test.Fatalf("Total = %d", got)
	}
}

func TestShowsNegativesAtLabOnlyForProcessedRolls(test *testing.T) {
	cases := []struct {
		status        string
		negativesAway bool
		want          bool
	}{
		{domain.RollStatusScanned, true, true},
		{domain.RollStatusDeveloped, true, true},
		{domain.RollStatusAtLab, true, false},
		{domain.RollStatusScanned, false, false},
	}
	for _, testCase := range cases {
		summary := Summary{gen.ListRollSummariesRow{Status: testCase.status, NegativesAtLab: testCase.negativesAway}}
		if got := summary.ShowsNegativesAtLab(); got != testCase.want {
			test.Errorf("status %s negatives=%v: got %v, want %v", testCase.status, testCase.negativesAway, got, testCase.want)
		}
	}
}

func TestRollDeleteSoftDeletesOnlyRollsStillInStock(test *testing.T) {
	fixture := newLoadFixture()
	ctx := context.Background()
	inUse := uuid.New()
	fixture.queries.Rolls[inUse] = gen.Roll{ID: inUse, Status: domain.RollStatusInCamera}

	if err := fixture.service.Delete(ctx, fixture.rollID); err != nil || !fixture.queries.Rolls[fixture.rollID].DeletedAt.Valid {
		test.Fatalf("err=%v roll=%+v", err, fixture.queries.Rolls[fixture.rollID])
	}
	if _, err := fixture.service.Detail(ctx, fixture.rollID); err == nil {
		test.Fatal("a deleted roll must be invisible")
	}
	if rolls, _ := fixture.service.List(ctx, Filter{}); len(rolls) != 1 || rolls[0].ID != inUse {
		test.Fatalf("a deleted roll must not be listed, got %v", rolls)
	}
	assertAppError(test, fixture.service.Delete(ctx, inUse), apperror.KindConflict, "roll_not_in_stock")
	assertAppError(test, fixture.service.Delete(ctx, fixture.rollID), apperror.KindNotFound, "not_found")
}
