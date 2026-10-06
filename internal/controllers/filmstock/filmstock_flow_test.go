package filmstock

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

func newController() (*Controller, *testutil.Memory) {
	memory := testutil.NewMemory()
	all := services.New(testutil.Store{Querier: memory}, testutil.NewMemoryFiles(), clock.Fixed{})
	return New(all.FilmStocks), memory
}

func stockInput() *api.FilmStockInput {
	return &api.FilmStockInput{Brand: "Kodak", Name: "Gold", Type: "color_negative", BoxIso: 200, Process: "C-41", Packaging: "roll"}
}

func TestPutFilmStockCreatesThenUpdates(test *testing.T) {
	controller, _ := newController()
	id := uuid.New()
	request := api.PutFilmStockRequestObject{Id: id, Body: stockInput()}

	first, err := controller.PutFilmStock(context.Background(), request)
	if created, ok := first.(api.PutFilmStock201JSONResponse); err != nil || !ok || created.Stock.Name != "Gold" {
		test.Fatalf("first=%#v err=%v", first, err)
	}
	second, _ := controller.PutFilmStock(context.Background(), request)
	if _, ok := second.(api.PutFilmStock200JSONResponse); !ok {
		test.Fatalf("second = %#v", second)
	}
	blank := stockInput()
	blank.Name = " "
	if _, err := controller.PutFilmStock(context.Background(), api.PutFilmStockRequestObject{Id: uuid.New(), Body: blank}); err == nil {
		test.Fatal("expected a validation error")
	}
}

func TestGetAndListFilmStocks(test *testing.T) {
	controller, memory := newController()
	ctx := context.Background()
	id := uuid.New()
	memory.Stocks[id] = gen.FilmStock{ID: id, Brand: "Kodak", Name: "Gold"}

	got, err := controller.GetFilmStock(ctx, api.GetFilmStockRequestObject{Id: id})
	if err != nil || got.(api.GetFilmStock200JSONResponse).Stock.Brand != "Kodak" {
		test.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := controller.GetFilmStock(ctx, api.GetFilmStockRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown stock must fail")
	}
	listed, err := controller.ListFilmStocks(ctx, api.ListFilmStocksRequestObject{Params: api.ListFilmStocksParams{Q: pointers.To("gold")}})
	if err != nil || len(listed.(api.ListFilmStocks200JSONResponse)) != 1 {
		test.Fatalf("listed=%#v err=%v", listed, err)
	}
}

func TestGetInventoryMapsFormatsAndExpiry(test *testing.T) {
	controller, memory := newController()
	id := uuid.New()
	memory.Stocks[id] = gen.FilmStock{ID: id, Brand: "Kodak", Name: "Gold"}
	memory.Inventory = []gen.InventoryRowsRow{{StockID: id, Format: 135, RollCount: 3}}
	memory.Expiries = []gen.SoonestExpiriesRow{{StockID: id, ExpiryYear: pointers.To(int32(2027)), ExpiryMonth: pointers.To(int32(6))}}

	colour, process := api.StockType("color_negative"), api.Process("C-41")
	response, err := controller.GetInventory(context.Background(), api.GetInventoryRequestObject{
		Params: api.GetInventoryParams{Type: &colour, Process: &process, Iso: pointers.To(200)},
	})
	if err != nil {
		test.Fatal(err)
	}
	items := response.(api.GetInventory200JSONResponse)
	if len(items) != 1 || items[0].Formats[0].Count != 3 || items[0].SoonestExpiry == nil || items[0].SoonestExpiry.Year != 2027 {
		test.Fatalf("items = %+v", items)
	}
}

func TestToAPIExpiryMonthHandlesAnUnknownMonth(test *testing.T) {
	if toAPIExpiryMonth(nil) != nil {
		test.Fatal("no expiry means nil")
	}
	if mapped := toAPIExpiryMonth(&domain.ExpiryMonth{Year: 2026}); mapped.Year != 2026 || mapped.Month != nil {
		test.Fatalf("mapped = %+v", mapped)
	}
}

func TestDeleteFilmStock(test *testing.T) {
	controller, memory := newController()
	ctx := context.Background()
	id := uuid.New()
	memory.Stocks[id] = gen.FilmStock{ID: id}

	response, err := controller.DeleteFilmStock(ctx, api.DeleteFilmStockRequestObject{Id: id})
	if _, ok := response.(api.DeleteFilmStock204Response); err != nil || !ok || !memory.Stocks[id].DeletedAt.Valid {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.DeleteFilmStock(ctx, api.DeleteFilmStockRequestObject{Id: id}); err == nil {
		test.Fatal("deleting a missing stock must fail")
	}
}

func TestCreateFilmStockAssignsAFreshID(test *testing.T) {
	controller, memory := newController()
	response, err := controller.CreateFilmStock(context.Background(), api.CreateFilmStockRequestObject{Body: stockInput()})
	if err != nil {
		test.Fatal(err)
	}
	if created := response.(api.CreateFilmStock201JSONResponse); created.Stock.Id == uuid.Nil || len(memory.Stocks) != 1 {
		test.Fatalf("created = %+v", created)
	}
	blank := stockInput()
	blank.Brand = ""
	if _, err := controller.CreateFilmStock(context.Background(), api.CreateFilmStockRequestObject{Body: blank}); err == nil {
		test.Fatal("expected a validation error")
	}
}
