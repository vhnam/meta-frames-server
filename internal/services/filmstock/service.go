package filmstock

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
}

func New(store db.Store) *Service { return &Service{store: store} }

// stockWarnings returns the non-blocking warnings of UC-10.
func stockWarnings(stock gen.FilmStock) []string {
	warnings := []string{}
	if stock.Type == "bw" && stock.Process != "BW" {
		warnings = append(warnings, "B&W stock usually uses the BW process")
	}
	if stock.Type == "slide" && stock.Process != "E-6" {
		warnings = append(warnings, "slide stock usually uses the E-6 process")
	}
	if stock.Packaging == "factory" && stock.PackOrigin != nil {
		warnings = append(warnings, "pack origin is normally empty for factory packaging")
	}
	return warnings
}

func buildStockDetail(ctx context.Context, queries gen.Querier, stock gen.FilmStock) (Detail, error) {
	detail := Detail{Stock: stock, Warnings: stockWarnings(stock), Siblings: []gen.FilmStock{}, Derived: []gen.FilmStock{}}
	if stock.BaseStockID != nil {
		base, err := queries.GetFilmStock(ctx, gen.GetFilmStockParams{ID: *stock.BaseStockID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return detail, err
		}
		detail.BaseStock = &base
		siblings, err := queries.ListSiblingStocks(ctx, gen.ListSiblingStocksParams{BaseStockID: stock.BaseStockID, ID: stock.ID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return detail, err
		}
		detail.Siblings = siblings
	}
	derived, err := queries.ListDerivedStocks(ctx, gen.ListDerivedStocksParams{BaseStockID: &stock.ID, OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return detail, err
	}
	detail.Derived = derived
	return detail, nil
}

func (service *Service) List(ctx context.Context, query *string) ([]gen.FilmStock, error) {
	return service.store.Queries().ListFilmStocks(ctx, gen.ListFilmStocksParams{OwnerID: requestctx.Owner(ctx), Q: pointers.TrimmedOrNil(query)})
}

// Get returns a stock with its base and related stocks (UC-13).
func (service *Service) Get(ctx context.Context, stockID uuid.UUID) (Detail, error) {
	queries := service.store.Queries()
	stock, err := queries.GetFilmStock(ctx, gen.GetFilmStockParams{ID: stockID, OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return Detail{}, shared.NotFoundOr(err, "film stock")
	}
	return buildStockDetail(ctx, queries, stock)
}

func validStockNames(input Input) (brand, name string, err error) {
	trimmedBrand, trimmedName := pointers.TrimmedOrNil(&input.Brand), pointers.TrimmedOrNil(&input.Name)
	if trimmedBrand == nil || trimmedName == nil {
		return "", "", apperror.Unprocessable("missing_fields", "brand and name are required")
	}
	return *trimmedBrand, *trimmedName, nil
}

// Create adds a film stock, optionally derived from a base stock (UC-10, UC-12).
func (service *Service) Create(ctx context.Context, stockID uuid.UUID, input Input) (Detail, error) {
	brand, name, err := validStockNames(input)
	if err != nil {
		return Detail{}, err
	}

	var detail Detail
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if err := checkBaseStock(ctx, queries, stockID, input.BaseStockID); err != nil {
			return err
		}
		saved, err := queries.InsertFilmStock(ctx, gen.InsertFilmStockParams{
			ID: stockID, Brand: brand, Name: name, Type: input.Type, BoxIso: int32(input.BoxISO),
			Process: input.Process, Packaging: input.Packaging,
			StockOrigin: pointers.TrimmedOrNil(input.StockOrigin), PackOrigin: pointers.TrimmedOrNil(input.PackOrigin),
			Description: pointers.TrimmedOrNil(input.Description), BaseStockID: input.BaseStockID,
			OwnerID: requestctx.Owner(ctx),
		})
		if err != nil {
			return err
		}
		detail, err = buildStockDetail(ctx, queries, saved)
		return err
	})
	if db.IsUniqueViolation(err) {
		return Detail{}, apperror.Conflict("already_exists", "a film stock with this id already exists")
	}
	return detail, err
}

// Update edits a film stock, including its base stock (UC-11, UC-12).
func (service *Service) Update(ctx context.Context, stockID uuid.UUID, input Input) (Detail, error) {
	brand, name, err := validStockNames(input)
	if err != nil {
		return Detail{}, err
	}

	var detail Detail
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if err := checkBaseStock(ctx, queries, stockID, input.BaseStockID); err != nil {
			return err
		}
		if _, err := queries.GetFilmStockForUpdate(ctx, gen.GetFilmStockForUpdateParams{ID: stockID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "film stock")
		}
		saved, err := queries.UpdateFilmStock(ctx, gen.UpdateFilmStockParams{
			ID: stockID, Brand: brand, Name: name, Type: input.Type, BoxIso: int32(input.BoxISO),
			Process: input.Process, Packaging: input.Packaging,
			StockOrigin: pointers.TrimmedOrNil(input.StockOrigin), PackOrigin: pointers.TrimmedOrNil(input.PackOrigin),
			Description: pointers.TrimmedOrNil(input.Description), BaseStockID: input.BaseStockID,
			OwnerID: requestctx.Owner(ctx),
		})
		if err != nil {
			return err
		}
		detail, err = buildStockDetail(ctx, queries, saved)
		return err
	})
	return detail, err
}

// checkBaseStock enforces UC-12: the base exists, is not the stock itself and forms no cycle.
func checkBaseStock(ctx context.Context, queries gen.Querier, stockID uuid.UUID, baseID *uuid.UUID) error {
	if baseID == nil {
		return nil
	}
	if *baseID == stockID {
		return apperror.Unprocessable("base_stock_self", "a stock cannot be its own base stock")
	}
	// Walk up from the proposed base; reaching this stock again means a cycle.
	visited := map[uuid.UUID]bool{}
	for current := baseID; current != nil; {
		if *current == stockID {
			return apperror.Unprocessable("base_stock_cycle", "base stock links cannot form a cycle")
		}
		if visited[*current] {
			break
		}
		visited[*current] = true
		stock, err := queries.GetFilmStock(ctx, gen.GetFilmStockParams{ID: *current, OwnerID: requestctx.Owner(ctx)})
		if db.IsNoRows(err) {
			return apperror.Unprocessable("unknown_base_stock", "base stock does not exist")
		}
		if err != nil {
			return err
		}
		current = stock.BaseStockID
	}
	return nil
}

// Inventory lists stocks that have unused rolls on hand (UC-14).
func (service *Service) Inventory(ctx context.Context, filter InventoryFilter) ([]InventoryItem, error) {
	queries := service.store.Queries()
	rows, err := queries.InventoryRows(ctx, gen.InventoryRowsParams{Type: filter.Type, Process: filter.Process, Iso: int32Pointer(filter.ISO), OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return nil, err
	}
	expiries, err := queries.SoonestExpiries(ctx, gen.SoonestExpiriesParams{Type: filter.Type, Process: filter.Process, Iso: int32Pointer(filter.ISO), OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return nil, err
	}
	stockIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if len(stockIDs) == 0 || stockIDs[len(stockIDs)-1] != row.StockID {
			stockIDs = append(stockIDs, row.StockID)
		}
	}
	stocks, err := queries.GetFilmStocksByIDs(ctx, gen.GetFilmStocksByIDsParams{Ids: stockIDs, OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return nil, err
	}
	return buildInventory(rows, expiries, stocks), nil
}

func int32Pointer(value *int) *int32 { return pointers.Int32(value) }

// buildInventory merges per-format counts and soonest expiries into one item per stock,
// keeping the order of stocks.
func buildInventory(rows []gen.InventoryRowsRow, expiries []gen.SoonestExpiriesRow, stocks []gen.FilmStock) []InventoryItem {
	formatsByStock := map[uuid.UUID][]FormatCount{}
	for _, row := range rows {
		formatsByStock[row.StockID] = append(formatsByStock[row.StockID], FormatCount{Format: row.Format, Count: row.RollCount})
	}
	soonestByStock := make(map[uuid.UUID]domain.ExpiryMonth, len(expiries))
	for _, expiry := range expiries {
		soonestByStock[expiry.StockID] = domain.ExpiryMonth{Year: *expiry.ExpiryYear, Month: expiry.ExpiryMonth}
	}

	items := make([]InventoryItem, 0, len(stocks))
	for _, stock := range stocks {
		item := InventoryItem{Stock: stock, Formats: formatsByStock[stock.ID]}
		if soonest, found := soonestByStock[stock.ID]; found {
			item.SoonestExpiry = &soonest
		}
		items = append(items, item)
	}
	return items
}

// Delete soft-deletes a stock that no roll and no derived stock refers to.
func (service *Service) Delete(ctx context.Context, stockID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetFilmStockForUpdate(ctx, gen.GetFilmStockForUpdateParams{ID: stockID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "film stock")
		}
		used, err := queries.FilmStockInUse(ctx, stockID)
		if err != nil {
			return err
		}
		if used {
			return apperror.Conflict("film_stock_in_use", "a film stock with rolls or derived stocks cannot be deleted")
		}
		_, err = queries.SoftDeleteFilmStock(ctx, gen.SoftDeleteFilmStockParams{ID: stockID, OwnerID: requestctx.Owner(ctx)})
		return err
	})
}
