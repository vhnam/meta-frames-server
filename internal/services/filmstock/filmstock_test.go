package filmstock

import (
	"context"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
	"testing"

	"github.com/google/uuid"
)

func TestFilmStockDelete(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	ctx := context.Background()
	free, withRoll, base, derived := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{free, withRoll, base} {
		queries.Stocks[id] = gen.FilmStock{ID: id}
	}
	queries.Stocks[derived] = gen.FilmStock{ID: derived, BaseStockID: &base}
	queries.Rolls[uuid.New()] = gen.Roll{FilmStockID: withRoll}

	if err := service.Delete(ctx, free); err != nil || !queries.Stocks[free].DeletedAt.Valid {
		test.Fatalf("err=%v stock=%+v", err, queries.Stocks[free])
	}
	if _, err := service.Get(ctx, free); err == nil {
		test.Fatal("a deleted stock must be invisible")
	}
	assertAppError(test, service.Delete(ctx, withRoll), apperror.KindConflict, "film_stock_in_use")
	assertAppError(test, service.Delete(ctx, base), apperror.KindConflict, "film_stock_in_use")
	assertAppError(test, service.Delete(ctx, uuid.New()), apperror.KindNotFound, "not_found")

	// Once the derived stock is deleted, its base is free to go.
	if err := service.Delete(ctx, derived); err != nil {
		test.Fatal(err)
	}
	if err := service.Delete(ctx, base); err != nil {
		test.Fatalf("a base without live derived stocks must be deletable: %v", err)
	}
}

func stockWithBase(id uuid.UUID, baseID *uuid.UUID) gen.FilmStock {
	return gen.FilmStock{ID: id, Name: id.String(), BaseStockID: baseID}
}

func TestCheckBaseStock(test *testing.T) {
	stockID, baseID, grandBaseID := uuid.New(), uuid.New(), uuid.New()
	queries := newMemoryQueries()
	queries.Stocks[baseID] = stockWithBase(baseID, &grandBaseID)
	queries.Stocks[grandBaseID] = stockWithBase(grandBaseID, nil)
	ctx := context.Background()

	if err := checkBaseStock(ctx, queries, stockID, nil); err != nil {
		test.Fatalf("no base rejected: %v", err)
	}
	if err := checkBaseStock(ctx, queries, stockID, &baseID); err != nil {
		test.Fatalf("valid base rejected: %v", err)
	}
	assertAppError(test, checkBaseStock(ctx, queries, stockID, &stockID), apperror.KindUnprocessable, "base_stock_self")

	missing := uuid.New()
	assertAppError(test, checkBaseStock(ctx, queries, stockID, &missing), apperror.KindUnprocessable, "unknown_base_stock")

	// Making grandBase point back at a stock whose chain reaches it forms a cycle.
	assertAppError(test, checkBaseStock(ctx, queries, grandBaseID, &baseID), apperror.KindUnprocessable, "base_stock_cycle")
}

func TestCheckBaseStockSurvivesAnExistingLoopInTheData(test *testing.T) {
	stockID, firstID, secondID := uuid.New(), uuid.New(), uuid.New()
	queries := newMemoryQueries()
	queries.Stocks[firstID] = stockWithBase(firstID, &secondID)
	queries.Stocks[secondID] = stockWithBase(secondID, &firstID)

	if err := checkBaseStock(context.Background(), queries, stockID, &firstID); err != nil {
		test.Fatalf("a pre-existing loop elsewhere must not hang or fail this check: %v", err)
	}
}

func TestFilmStockCreateReportsWarnings(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})

	detail, err := service.Create(context.Background(), uuid.New(), Input{
		Brand: " Ilford ", Name: "HP5", Type: "bw", BoxISO: 400, Process: "C-41", Packaging: "factory",
	})
	if err != nil {
		test.Fatal(err)
	}
	if detail.Stock.Brand != "Ilford" || len(detail.Warnings) != 1 || len(queries.Stocks) != 1 {
		test.Fatalf("detail = %+v", detail)
	}
}

func TestFilmStockUpdateEditsInPlaceAndNeedsAnExistingStock(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	stockID := uuid.New()
	queries.Stocks[stockID] = gen.FilmStock{ID: stockID, Brand: "Ilford", Name: "HP5"}

	detail, err := service.Update(context.Background(), stockID, Input{Brand: "Ilford", Name: "HP5 Plus", Type: "bw", BoxISO: 400, Process: "BW", Packaging: "factory"})
	if err != nil || detail.Stock.Name != "HP5 Plus" || len(queries.Stocks) != 1 {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
	_, err = service.Update(context.Background(), uuid.New(), Input{Brand: "a", Name: "b"})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestFilmStockCreateAndUpdateRequireBrandAndName(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()})
	_, err := service.Create(context.Background(), uuid.New(), Input{Brand: " ", Name: "HP5"})
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
	_, err = service.Update(context.Background(), uuid.New(), Input{Brand: "Ilford", Name: " "})
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
}

func TestFilmStockDetailListsSiblingsAndDerivedStocks(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	baseID, firstID, secondID := uuid.New(), uuid.New(), uuid.New()
	queries.Stocks[baseID] = stockWithBase(baseID, nil)
	queries.Stocks[firstID] = stockWithBase(firstID, &baseID)
	queries.Stocks[secondID] = stockWithBase(secondID, &baseID)

	fromRepack, err := service.Get(context.Background(), firstID)
	if err != nil {
		test.Fatal(err)
	}
	if fromRepack.BaseStock == nil || fromRepack.BaseStock.ID != baseID || len(fromRepack.Siblings) != 1 || fromRepack.Siblings[0].ID != secondID {
		test.Fatalf("repack detail = %+v", fromRepack)
	}
	fromBase, err := service.Get(context.Background(), baseID)
	if err != nil {
		test.Fatal(err)
	}
	if fromBase.BaseStock != nil || len(fromBase.Derived) != 2 {
		test.Fatalf("base detail = %+v", fromBase)
	}
	_, err = service.Get(context.Background(), uuid.New())
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestBuildInventoryMergesFormatsAndSoonestExpiry(test *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	rows := []gen.InventoryRowsRow{
		{StockID: firstID, Format: 120, RollCount: 1},
		{StockID: firstID, Format: 135, RollCount: 4},
		{StockID: secondID, Format: 135, RollCount: 2},
	}
	expiries := []gen.SoonestExpiriesRow{
		{StockID: firstID, ExpiryYear: int32Ref(2026), ExpiryMonth: nil},
	}
	stocks := []gen.FilmStock{{ID: firstID}, {ID: secondID}}

	items := buildInventory(rows, expiries, stocks)
	if len(items) != 2 {
		test.Fatalf("items = %d", len(items))
	}
	if len(items[0].Formats) != 2 || items[0].Formats[1].Count != 4 {
		test.Fatalf("first formats = %+v", items[0].Formats)
	}
	if items[0].SoonestExpiry == nil || items[0].SoonestExpiry.Year != 2026 || items[0].SoonestExpiry.Month != nil {
		test.Fatalf("first expiry = %+v (an unknown month must stay nil)", items[0].SoonestExpiry)
	}
	if items[1].SoonestExpiry != nil {
		test.Fatalf("second stock has no dated rolls, got %+v", items[1].SoonestExpiry)
	}
}

func TestFilmStockListGetAndInventory(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	ctx := context.Background()
	stockID := uuid.New()
	queries.Stocks[stockID] = gen.FilmStock{ID: stockID, Brand: "Kodak", Name: "Gold"}

	if stocks, err := service.List(ctx, pointers.To(" gold ")); err != nil || len(stocks) != 1 {
		test.Fatalf("stocks=%v err=%v", stocks, err)
	}
	if detail, err := service.Get(ctx, stockID); err != nil || detail.Stock.Brand != "Kodak" {
		test.Fatalf("detail=%+v err=%v", detail, err)
	}
	if _, err := service.Get(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown stock must fail")
	}

	queries.Inventory = []gen.InventoryRowsRow{{StockID: stockID, Format: 135, RollCount: 3}, {StockID: stockID, Format: 120, RollCount: 1}}
	queries.Expiries = []gen.SoonestExpiriesRow{{StockID: stockID, ExpiryYear: pointers.To(int32(2027)), ExpiryMonth: pointers.To(int32(6))}}
	items, err := service.Inventory(ctx, InventoryFilter{ISO: pointers.To(400)})
	if err != nil || len(items) != 1 || len(items[0].Formats) != 2 || items[0].SoonestExpiry == nil {
		test.Fatalf("items=%+v err=%v", items, err)
	}
}

// ---- cameras ----

func TestStockWarnings(test *testing.T) {
	cases := []struct {
		name  string
		stock gen.FilmStock
		want  int
	}{
		{"color C-41 factory", gen.FilmStock{Type: "color", Process: "C-41", Packaging: "factory"}, 0},
		{"bw with C-41", gen.FilmStock{Type: "bw", Process: "C-41", Packaging: "factory"}, 1},
		{"slide with C-41", gen.FilmStock{Type: "slide", Process: "C-41", Packaging: "factory"}, 1},
		{"factory with pack origin", gen.FilmStock{Type: "color", Process: "C-41", Packaging: "factory", PackOrigin: stringRef("x")}, 1},
		{"repack with pack origin", gen.FilmStock{Type: "color", Process: "C-41", Packaging: "repack", PackOrigin: stringRef("x")}, 0},
		{"two problems", gen.FilmStock{Type: "bw", Process: "E-6", Packaging: "factory", PackOrigin: stringRef("x")}, 2},
	}
	for _, testCase := range cases {
		if got := stockWarnings(testCase.stock); len(got) != testCase.want {
			test.Errorf("%s: warnings = %v, want %d", testCase.name, got, testCase.want)
		}
	}
}
