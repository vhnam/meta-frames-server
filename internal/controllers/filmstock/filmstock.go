package filmstock

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	filmstocksvc "meta-frames-server/internal/services/filmstock"
)

// Controller serves the filmstock endpoints.
type Controller struct {
	filmStocks *filmstocksvc.Service
}

// New builds the controller around its service.
func New(filmStocks *filmstocksvc.Service) *Controller {
	return &Controller{filmStocks: filmStocks}
}

func ToAPIFilmStock(stock gen.FilmStock) api.FilmStock {
	return api.FilmStock{
		Id: stock.ID, Brand: stock.Brand, Name: stock.Name, Type: api.StockType(stock.Type), BoxIso: int(stock.BoxIso),
		Process: api.Process(stock.Process), Packaging: api.Packaging(stock.Packaging),
		StockOrigin: stock.StockOrigin, PackOrigin: stock.PackOrigin, Description: stock.Description,
		BaseStockId: stock.BaseStockID,
		CreatedAt:   convert.Timestamp(stock.CreatedAt), UpdatedAt: convert.Timestamp(stock.UpdatedAt),
	}
}

func toAPIFilmStocks(stocks []gen.FilmStock) []api.FilmStock {
	mapped := make([]api.FilmStock, len(stocks))
	for index, stock := range stocks {
		mapped[index] = ToAPIFilmStock(stock)
	}
	return mapped
}

func toAPIFilmStockDetail(detail filmstocksvc.Detail) api.FilmStockDetail {
	mapped := api.FilmStockDetail{
		Stock: ToAPIFilmStock(detail.Stock), Siblings: toAPIFilmStocks(detail.Siblings),
		Derived: toAPIFilmStocks(detail.Derived), Warnings: detail.Warnings,
	}
	if detail.BaseStock != nil {
		mapped.BaseStock = pointers.To(ToAPIFilmStock(*detail.BaseStock))
	}
	return mapped
}

func toAPIExpiryMonth(expiry *domain.ExpiryMonth) *api.ExpiryMonth {
	if expiry == nil {
		return nil
	}
	return &api.ExpiryMonth{Year: int(expiry.Year), Month: pointers.Int(expiry.Month)}
}

func (controller *Controller) ListFilmStocks(ctx context.Context, request api.ListFilmStocksRequestObject) (api.ListFilmStocksResponseObject, error) {
	stocks, err := controller.filmStocks.List(ctx, request.Params.Q)
	if err != nil {
		return nil, err
	}
	return api.ListFilmStocks200JSONResponse(toAPIFilmStocks(stocks)), nil
}

func (controller *Controller) GetFilmStock(ctx context.Context, request api.GetFilmStockRequestObject) (api.GetFilmStockResponseObject, error) {
	detail, err := controller.filmStocks.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetFilmStock200JSONResponse(toAPIFilmStockDetail(detail)), nil
}

func toFilmStockInput(body *api.FilmStockInput) filmstocksvc.Input {
	return filmstocksvc.Input{
		Brand: body.Brand, Name: body.Name, Type: string(body.Type), BoxISO: body.BoxIso,
		Process: string(body.Process), Packaging: string(body.Packaging),
		StockOrigin: body.StockOrigin, PackOrigin: body.PackOrigin, Description: body.Description,
		BaseStockID: body.BaseStockId,
	}
}

func (controller *Controller) CreateFilmStock(ctx context.Context, request api.CreateFilmStockRequestObject) (api.CreateFilmStockResponseObject, error) {
	detail, err := controller.filmStocks.Create(ctx, uuid.New(), toFilmStockInput(request.Body))
	if err != nil {
		return nil, err
	}
	return api.CreateFilmStock201JSONResponse(toAPIFilmStockDetail(detail)), nil
}

func (controller *Controller) PutFilmStock(ctx context.Context, request api.PutFilmStockRequestObject) (api.PutFilmStockResponseObject, error) {
	input := toFilmStockInput(request.Body)
	detail, err := controller.filmStocks.Update(ctx, request.Id, input)
	if apperror.IsNotFound(err) {
		if detail, err = controller.filmStocks.Create(ctx, request.Id, input); err != nil {
			return nil, err
		}
		return api.PutFilmStock201JSONResponse(toAPIFilmStockDetail(detail)), nil
	}
	if err != nil {
		return nil, err
	}
	return api.PutFilmStock200JSONResponse(toAPIFilmStockDetail(detail)), nil
}

func (controller *Controller) GetInventory(ctx context.Context, request api.GetInventoryRequestObject) (api.GetInventoryResponseObject, error) {
	filter := filmstocksvc.InventoryFilter{ISO: request.Params.Iso}
	if request.Params.Type != nil {
		filter.Type = pointers.To(string(*request.Params.Type))
	}
	if request.Params.Process != nil {
		filter.Process = pointers.To(string(*request.Params.Process))
	}
	items, err := controller.filmStocks.Inventory(ctx, filter)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.InventoryItem, len(items))
	for index, item := range items {
		formats := make([]api.InventoryFormatCount, len(item.Formats))
		for formatIndex, format := range item.Formats {
			formats[formatIndex] = api.InventoryFormatCount{Format: int(format.Format), Count: int(format.Count)}
		}
		mapped[index] = api.InventoryItem{Stock: ToAPIFilmStock(item.Stock), Formats: formats, SoonestExpiry: toAPIExpiryMonth(item.SoonestExpiry)}
	}
	return api.GetInventory200JSONResponse(mapped), nil
}

func (controller *Controller) DeleteFilmStock(ctx context.Context, request api.DeleteFilmStockRequestObject) (api.DeleteFilmStockResponseObject, error) {
	if err := controller.filmStocks.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteFilmStock204Response{}, nil
}
