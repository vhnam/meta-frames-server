package processing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

type fixture struct {
	controller *Controller
	memory     *testutil.Memory
	rollID     uuid.UUID
	labID      uuid.UUID
}

func newFixture() fixture {
	memory := testutil.NewMemory()
	all := services.New(testutil.Store{Querier: memory}, testutil.NewMemoryFiles(), clock.Fixed{Date: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	stockID, rollID, labID := uuid.New(), uuid.New(), uuid.New()
	memory.Stocks[stockID] = gen.FilmStock{ID: stockID, Process: "C-41"}
	memory.Rolls[rollID] = gen.Roll{ID: rollID, FilmStockID: stockID, Status: domain.RollStatusDoneShooting}
	memory.Labs[labID] = gen.Lab{ID: labID, Name: "Lab A"}
	return fixture{controller: New(all.Processing), memory: memory, rollID: rollID, labID: labID}
}

func (f fixture) send(test *testing.T, jobID uuid.UUID) {
	test.Helper()
	_, err := f.controller.PutProcessing(context.Background(), api.PutProcessingRequestObject{
		Id: f.rollID, JobId: jobID, Body: &api.ProcessingInput{Type: "develop_scan", LabId: &f.labID},
	})
	if err != nil {
		test.Fatal(err)
	}
}

func TestPutProcessingCreatesThenUpdates(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	jobID := uuid.New()
	process := api.Process("E-6")
	request := api.PutProcessingRequestObject{Id: f.rollID, JobId: jobID, Body: &api.ProcessingInput{Type: "develop_scan", Process: &process, LabId: &f.labID}}

	first, err := f.controller.PutProcessing(ctx, request)
	if created, ok := first.(api.PutProcessing201JSONResponse); err != nil || !ok || created.Process != "E-6" {
		test.Fatalf("first=%#v err=%v", first, err)
	}
	second, _ := f.controller.PutProcessing(ctx, request)
	if _, ok := second.(api.PutProcessing200JSONResponse); !ok {
		test.Fatalf("second = %#v", second)
	}
	if _, err := f.controller.PutProcessing(ctx, api.PutProcessingRequestObject{Id: uuid.New(), JobId: uuid.New(), Body: request.Body}); err == nil {
		test.Fatal("an unknown roll must fail")
	}
}

func TestListAndGetProcessing(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	jobID := uuid.New()
	f.send(test, jobID)

	listed, err := f.controller.ListRollProcessing(ctx, api.ListRollProcessingRequestObject{Id: f.rollID})
	if err != nil || len(listed.(api.ListRollProcessing200JSONResponse)) != 1 {
		test.Fatalf("listed=%#v err=%v", listed, err)
	}
	got, err := f.controller.GetProcessing(ctx, api.GetProcessingRequestObject{Id: jobID})
	if err != nil || got.(api.GetProcessing200JSONResponse).Id != jobID {
		test.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := f.controller.ListRollProcessing(ctx, api.ListRollProcessingRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown roll must fail")
	}
	if _, err := f.controller.GetProcessing(ctx, api.GetProcessingRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown job must fail")
	}
}

func TestRecordScansReceivedAndNegativesReturned(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	jobID := uuid.New()
	f.send(test, jobID)

	given := convert.APIDate(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	received, err := f.controller.RecordScansReceived(ctx, api.RecordScansReceivedRequestObject{Id: jobID, Body: &api.DateInput{Date: &given}})
	if err != nil || received.(api.RecordScansReceived200JSONResponse).ScansReceivedAt == nil {
		test.Fatalf("received=%#v err=%v", received, err)
	}
	returned, err := f.controller.RecordNegativesReturned(ctx, api.RecordNegativesReturnedRequestObject{Id: jobID})
	if err != nil || returned.(api.RecordNegativesReturned200JSONResponse).NegativesReturnedAt == nil {
		test.Fatalf("returned=%#v err=%v", returned, err)
	}
	if _, err := f.controller.RecordScansReceived(ctx, api.RecordScansReceivedRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown job must fail")
	}
	if _, err := f.controller.RecordNegativesReturned(ctx, api.RecordNegativesReturnedRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown job must fail")
	}
}

func TestListNegativesAtLab(test *testing.T) {
	f := newFixture()
	f.memory.AtLabRows = []gen.NegativesAtLabRow{{
		ID: uuid.New(), RollID: f.rollID, LabName: "Lab A", Type: "develop", SentAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		DaysSinceSent: 31, StockBrand: "Kodak", StockName: "Gold",
	}}
	response, err := f.controller.ListNegativesAtLab(context.Background(), api.ListNegativesAtLabRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	items := response.(api.ListNegativesAtLab200JSONResponse)
	if len(items) != 1 || items[0].StockName != "Kodak Gold" || items[0].DaysSinceSent != 31 {
		test.Fatalf("items = %+v", items)
	}
}

func TestDeleteProcessing(test *testing.T) {
	f := newFixture()
	ctx := context.Background()
	jobID := uuid.New()
	f.send(test, jobID)

	response, err := f.controller.DeleteProcessing(ctx, api.DeleteProcessingRequestObject{Id: jobID})
	if _, ok := response.(api.DeleteProcessing204Response); err != nil || !ok {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := f.controller.GetProcessing(ctx, api.GetProcessingRequestObject{Id: jobID}); err == nil {
		test.Fatal("a deleted job must be invisible")
	}
	if _, err := f.controller.DeleteProcessing(ctx, api.DeleteProcessingRequestObject{Id: jobID}); err == nil {
		test.Fatal("deleting a missing job must fail")
	}
}
