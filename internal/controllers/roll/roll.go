package roll

import (
	"context"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/controllers/filmstock"
	"meta-frames-server/internal/controllers/lens"
	"meta-frames-server/internal/controllers/processing"
	"meta-frames-server/internal/controllers/scan"
	rollsvc "meta-frames-server/internal/services/roll"
)

// Controller serves the roll endpoints.
type Controller struct {
	rolls *rollsvc.Service
}

// New builds the controller around its service.
func New(rolls *rollsvc.Service) *Controller { return &Controller{rolls: rolls} }

func ToAPIRollSummary(summary rollsvc.Summary) api.RollSummary {
	mapped := api.RollSummary{
		Id: summary.ID, FilmStockId: summary.FilmStockID, StockBrand: summary.StockBrand, StockName: summary.StockName,
		CameraId: summary.CameraID, Format: int(summary.Format), Exposures: int(summary.Exposures),
		Status: api.RollStatus(summary.Status), ShotIso: pointers.Int(summary.ShotIso), Price: pointers.Int(summary.Price),
		Description: summary.Description, StartedAt: convert.ToAPIDate(summary.StartedAt), FinishedAt: convert.ToAPIDate(summary.FinishedAt),
		NegativesAtLab: summary.ShowsNegativesAtLab(),
	}
	if summary.CameraBrand != nil && summary.CameraModel != nil {
		mapped.CameraName = pointers.To(*summary.CameraBrand + " " + *summary.CameraModel)
	}
	if summary.ExpiryYear != nil {
		mapped.Expiry = &api.ExpiryMonth{Year: int(*summary.ExpiryYear), Month: pointers.Int(summary.ExpiryMonth)}
	}
	return mapped
}

func toAPIRollSummaries(summaries []rollsvc.Summary) []api.RollSummary {
	mapped := make([]api.RollSummary, len(summaries))
	for index, summary := range summaries {
		mapped[index] = ToAPIRollSummary(summary)
	}
	return mapped
}

func toAPIRollDetail(detail rollsvc.Detail) api.RollDetail {
	mapped := api.RollDetail{
		Roll: ToAPIRollSummary(detail.Summary), Stock: filmstock.ToAPIFilmStock(detail.Stock), Lenses: lens.ToAPILenses(detail.Lenses),
		Processing: make([]api.Processing, len(detail.Processing)), Frames: make([]api.Frame, len(detail.Frames)),
		Totals: api.RollTotals{
			RollPrice: int(detail.Totals.RollPrice), ProcessingPrice: int(detail.Totals.ProcessingPrice),
			Total: int(detail.Totals.Total()), Incomplete: detail.Totals.Incomplete,
		},
		Warnings: detail.Warnings,
	}
	if detail.BaseStock != nil {
		mapped.BaseStock = pointers.To(filmstock.ToAPIFilmStock(*detail.BaseStock))
	}
	for index, view := range detail.Processing {
		mapped.Processing[index] = processing.ToAPIProcessing(view)
	}
	for index, view := range detail.Frames {
		mapped.Frames[index] = scan.ToAPIFrame(view)
	}
	return mapped
}

func (controller *Controller) ListRolls(ctx context.Context, request api.ListRollsRequestObject) (api.ListRollsResponseObject, error) {
	params := request.Params
	filter := rollsvc.Filter{
		FilmStockID: params.FilmStockId, CameraID: params.CameraId, LensID: params.LensId, Format: params.Format,
		StartedFrom: convert.FromAPIDate(params.StartedFrom), StartedTo: convert.FromAPIDate(params.StartedTo),
	}
	if params.Status != nil {
		filter.Status = pointers.To(string(*params.Status))
	}
	summaries, err := controller.rolls.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return api.ListRolls200JSONResponse(toAPIRollSummaries(summaries)), nil
}

func (controller *Controller) GetRoll(ctx context.Context, request api.GetRollRequestObject) (api.GetRollResponseObject, error) {
	detail, err := controller.rolls.Detail(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetRoll200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) AddRolls(ctx context.Context, request api.AddRollsRequestObject) (api.AddRollsResponseObject, error) {
	body := request.Body
	input := rollsvc.BulkInput{
		FilmStockID: body.FilmStockId, Format: body.Format, Exposures: body.Exposures, Quantity: body.Quantity,
		Price: body.Price, ExpiryYear: body.ExpiryYear, ExpiryMonth: body.ExpiryMonth,
	}
	if request.Params.IdempotencyKey != nil {
		input.IdempotencyKey = *request.Params.IdempotencyKey
	}
	summaries, err := controller.rolls.AddBulk(ctx, input)
	if err != nil {
		return nil, err
	}
	return api.AddRolls201JSONResponse(toAPIRollSummaries(summaries)), nil
}

func (controller *Controller) PutRoll(ctx context.Context, request api.PutRollRequestObject) (api.PutRollResponseObject, error) {
	body := request.Body
	detail, err := controller.rolls.Update(ctx, request.Id, rollsvc.Input{
		FilmStockID: body.FilmStockId, Format: body.Format, Exposures: body.Exposures, Price: body.Price,
		ExpiryYear: body.ExpiryYear, ExpiryMonth: body.ExpiryMonth, ShotISO: body.ShotIso,
		StartedAt: convert.FromAPIDate(body.StartedAt), FinishedAt: convert.FromAPIDate(body.FinishedAt), Description: body.Description,
	})
	if err != nil {
		return nil, err
	}
	return api.PutRoll200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) LoadRoll(ctx context.Context, request api.LoadRollRequestObject) (api.LoadRollResponseObject, error) {
	body := request.Body
	detail, err := controller.rolls.Load(ctx, request.Id, rollsvc.LoadInput{
		CameraID: body.CameraId, StartedAt: convert.FromAPIDate(body.StartedAt), ShotISO: body.ShotIso, LensIDs: body.LensIds,
	})
	if err != nil {
		return nil, err
	}
	return api.LoadRoll200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) SetRollLenses(ctx context.Context, request api.SetRollLensesRequestObject) (api.SetRollLensesResponseObject, error) {
	lenses, err := controller.rolls.SetLenses(ctx, request.Id, request.Body.LensIds)
	if err != nil {
		return nil, err
	}
	return api.SetRollLenses200JSONResponse(lens.ToAPILenses(lenses)), nil
}

func (controller *Controller) FinishRoll(ctx context.Context, request api.FinishRollRequestObject) (api.FinishRollResponseObject, error) {
	var finishedAt *api.DateInput
	if request.Body != nil {
		finishedAt = request.Body
	}
	detail, err := controller.rolls.Finish(ctx, request.Id, convert.OptionalDate(finishedAt))
	if err != nil {
		return nil, err
	}
	return api.FinishRoll200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) GetExpiry(ctx context.Context, _ api.GetExpiryRequestObject) (api.GetExpiryResponseObject, error) {
	report, err := controller.rolls.ExpiryReport(ctx)
	if err != nil {
		return nil, err
	}
	mapped := api.ExpiryView{Expiring: make([]api.ExpiryRoll, len(report.Expiring)), NoExpiry: toAPIRollSummaries(report.NoExpiry)}
	for index, expiring := range report.Expiring {
		mapped.Expiring[index] = api.ExpiryRoll{Roll: ToAPIRollSummary(expiring.Roll), ExpiresOn: convert.APIDate(expiring.ExpiresOn), Expired: expiring.Expired}
	}
	return api.GetExpiry200JSONResponse(mapped), nil
}

func (controller *Controller) DeleteRoll(ctx context.Context, request api.DeleteRollRequestObject) (api.DeleteRollResponseObject, error) {
	if err := controller.rolls.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteRoll204Response{}, nil
}
