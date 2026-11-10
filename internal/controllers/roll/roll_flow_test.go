package roll

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services"
	rollsvc "meta-frames-server/internal/services/roll"
	"meta-frames-server/internal/testutil"
)

type fixture struct {
	controller *Controller
	memory     *testutil.Memory
	stockID    uuid.UUID
	rollID     uuid.UUID
	cameraID   uuid.UUID
}

func newFixture() fixture {
	memory := testutil.NewMemory()
	all := services.New(testutil.Store{Querier: memory}, testutil.NewMemoryFiles(), clock.Fixed{Date: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	stockID, rollID, cameraID := uuid.New(), uuid.New(), uuid.New()
	memory.Stocks[stockID] = gen.FilmStock{ID: stockID, Brand: "Kodak", Name: "Gold", BoxIso: "200"}
	memory.Rolls[rollID] = gen.Roll{ID: rollID, FilmStockID: stockID, Status: domain.RollStatusInStock, Format: 135, Exposures: 36}
	memory.Cameras[cameraID] = gen.Camera{ID: cameraID, IsActive: true, Mount: pointers.To("F")}
	return fixture{controller: New(all.Rolls), memory: memory, stockID: stockID, rollID: rollID, cameraID: cameraID}
}

func TestAddRollsCreatesRollsAndHonoursTheIdempotencyKey(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	key := "abc"
	request := api.AddRollsRequestObject{
		Params: api.AddRollsParams{IdempotencyKey: &key},
		Body:   &api.BulkRollsInput{FilmStockId: f.stockID, Format: 135, Quantity: 2},
	}
	first, err := f.controller.AddRolls(ctx, request)
	if err != nil || len(first.(api.AddRolls201JSONResponse)) != 2 {
		test.Fatalf("first=%#v err=%v", first, err)
	}
	total := len(f.memory.Rolls)
	if _, err := f.controller.AddRolls(ctx, request); err != nil || len(f.memory.Rolls) != total {
		test.Fatalf("a retry must not create more rolls (%d vs %d) err=%v", len(f.memory.Rolls), total, err)
	}
	if _, err := f.controller.AddRolls(ctx, api.AddRollsRequestObject{Body: &api.BulkRollsInput{FilmStockId: uuid.New(), Format: 135, Quantity: 1}}); err == nil {
		test.Fatal("an unknown stock must fail")
	}
}

func TestListAndGetRolls(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	status := api.RollStatus(domain.RollStatusInStock)
	from := convert.APIDate(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	listed, err := f.controller.ListRolls(ctx, api.ListRollsRequestObject{Params: api.ListRollsParams{Status: &status, StartedFrom: &from}})
	if err != nil || len(listed.(api.ListRolls200JSONResponse)) != 1 {
		test.Fatalf("listed=%#v err=%v", listed, err)
	}
	got, err := f.controller.GetRoll(ctx, api.GetRollRequestObject{Id: f.rollID})
	if err != nil || got.(api.GetRoll200JSONResponse).Roll.Id != f.rollID {
		test.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := f.controller.GetRoll(ctx, api.GetRollRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown roll must fail")
	}
}

func TestPutRollUpdatesTheRoll(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	response, err := f.controller.PutRoll(ctx, api.PutRollRequestObject{Id: f.rollID, Body: &api.RollInput{FilmStockId: f.stockID, Format: 120, Exposures: 12}})
	if err != nil || response.(api.PutRoll200JSONResponse).Roll.Format != 120 {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := f.controller.PutRoll(ctx, api.PutRollRequestObject{Id: uuid.New(), Body: &api.RollInput{FilmStockId: f.stockID, Format: 135, Exposures: 36}}); err == nil {
		test.Fatal("an unknown roll must fail")
	}
}

func TestLoadRollLensesAndFinish(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	lensID := uuid.New()
	f.memory.Lenses[lensID] = gen.Lens{ID: lensID, IsActive: true}

	loaded, err := f.controller.LoadRoll(ctx, api.LoadRollRequestObject{Id: f.rollID, Body: &api.LoadRollInput{CameraId: f.cameraID, LensIds: &[]uuid.UUID{lensID}}})
	if err != nil || loaded.(api.LoadRoll200JSONResponse).Roll.Status != api.RollStatus(domain.RollStatusInCamera) {
		test.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err := f.controller.LoadRoll(ctx, api.LoadRollRequestObject{Id: uuid.New(), Body: &api.LoadRollInput{CameraId: f.cameraID}}); err == nil {
		test.Fatal("an unknown roll must fail")
	}

	lenses, err := f.controller.SetRollLenses(ctx, api.SetRollLensesRequestObject{Id: f.rollID, Body: &api.CameraLensesInput{LensIds: []uuid.UUID{lensID}}})
	if err != nil || len(lenses.(api.SetRollLenses200JSONResponse)) != 1 {
		test.Fatalf("lenses=%#v err=%v", lenses, err)
	}
	if _, err := f.controller.SetRollLenses(ctx, api.SetRollLensesRequestObject{Id: uuid.New(), Body: &api.CameraLensesInput{}}); err == nil {
		test.Fatal("an unknown roll must fail")
	}

	finished, err := f.controller.FinishRoll(ctx, api.FinishRollRequestObject{Id: f.rollID})
	if err != nil || finished.(api.FinishRoll200JSONResponse).Roll.Status != api.RollStatus(domain.RollStatusDoneShooting) {
		test.Fatalf("finished=%#v err=%v", finished, err)
	}
	if _, err := f.controller.FinishRoll(ctx, api.FinishRollRequestObject{Id: f.rollID, Body: &api.DateInput{}}); err != nil {
		test.Fatalf("finishing again on the same day is a no-op: %v", err)
	}
	otherDay := convert.APIDate(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if _, err := f.controller.FinishRoll(ctx, api.FinishRollRequestObject{Id: f.rollID, Body: &api.DateInput{Date: &otherDay}}); err == nil {
		test.Fatal("finishing again on another day must fail")
	}
}

func TestPutRollChecksIfMatchAndReportsTheVersion(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	put := func(ifMatch *string) (api.PutRollResponseObject, error) {
		return f.controller.PutRoll(ctx, api.PutRollRequestObject{
			Id: f.rollID, Params: api.PutRollParams{IfMatch: ifMatch}, Body: &api.RollInput{FilmStockId: f.stockID, Format: 135, Exposures: 36, Price: pointers.To(100)},
		})
	}
	response, err := put(pointers.To(`"0"`))
	if err != nil || response.(api.PutRoll200JSONResponse).Roll.Version != 1 {
		test.Fatalf("matching version: response=%#v err=%v", response, err)
	}
	_, err = put(pointers.To(`"0"`))
	testutil.AssertAppError(test, err, apperror.KindPreconditionFailed, "roll_changed")
	for _, accepted := range []string{`"1"`, ` "9", "1" `, `*`} {
		if _, err := put(pointers.To(accepted)); err != nil {
			test.Fatalf("If-Match %s: %v", accepted, err)
		}
	}
	for _, malformed := range []string{``, `1`, `W/"1"`, `"1`, `"-1"`, `"+1"`, `"1",`, `"x"`, `"99999999999999999999"`} {
		_, err := put(pointers.To(malformed))
		testutil.AssertAppError(test, err, apperror.KindBadRequest, "invalid_precondition")
	}
	if _, err := put(nil); err != nil {
		test.Fatalf("a client without If-Match still writes: %v", err)
	}
}

func TestPutRollNotesWritesOnlyTheNotes(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	roll := f.memory.Rolls[f.rollID]
	roll.Price = pointers.To(int32(150000))
	f.memory.Rolls[f.rollID] = roll

	response, err := f.controller.PutRollNotes(ctx, api.PutRollNotesRequestObject{Id: f.rollID, Body: &api.RollNotesInput{Description: "overcast", BaseDescription: pointers.To("")}})
	detail, ok := response.(api.PutRollNotes200JSONResponse)
	if err != nil || !ok || *detail.Roll.Description != "overcast" || *detail.Roll.Price != 150000 || detail.Roll.Version != 1 {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	_, err = f.controller.PutRollNotes(ctx, api.PutRollNotesRequestObject{Id: f.rollID, Body: &api.RollNotesInput{Description: "mine", BaseDescription: pointers.To("")}})
	testutil.AssertAppError(test, err, apperror.KindConflict, "roll_notes_conflict")
}

func TestGetExpiryReport(test *testing.T) {
	f := newFixture()
	roll := f.memory.Rolls[f.rollID]
	roll.ExpiryYear, roll.ExpiryMonth = pointers.To(int32(2026)), pointers.To(int32(11))
	f.memory.Rolls[f.rollID] = roll
	noExpiry := uuid.New()
	f.memory.Rolls[noExpiry] = gen.Roll{ID: noExpiry, FilmStockID: f.stockID, Status: domain.RollStatusInStock}

	response, err := f.controller.GetExpiry(context.Background(), api.GetExpiryRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	report := response.(api.GetExpiry200JSONResponse)
	if len(report.Expiring) != 1 || len(report.NoExpiry) != 1 || report.Expiring[0].Expired {
		test.Fatalf("report = %+v", report)
	}
}

func TestToAPIRollSummariesKeepsTheOrder(test *testing.T) {
	mapped := toAPIRollSummaries([]rollsvc.Summary{{ListRollSummariesRow: gen.ListRollSummariesRow{Format: 135}}, {ListRollSummariesRow: gen.ListRollSummariesRow{Format: 120}}})
	if len(mapped) != 2 || mapped[1].Format != 120 {
		test.Fatalf("mapped = %+v", mapped)
	}
}

func TestDeleteRoll(test *testing.T) {
	f := newFixture()
	ctx := context.Background()

	response, err := f.controller.DeleteRoll(ctx, api.DeleteRollRequestObject{Id: f.rollID})
	if _, ok := response.(api.DeleteRoll204Response); err != nil || !ok || !f.memory.Rolls[f.rollID].DeletedAt.Valid {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := f.controller.DeleteRoll(ctx, api.DeleteRollRequestObject{Id: f.rollID}); err == nil {
		test.Fatal("deleting a missing roll must fail")
	}
	loaded := uuid.New()
	f.memory.Rolls[loaded] = gen.Roll{ID: loaded, Status: domain.RollStatusInCamera}
	if _, err := f.controller.DeleteRoll(ctx, api.DeleteRollRequestObject{Id: loaded}); err == nil {
		test.Fatal("a loaded roll must not be deleted")
	}
}
