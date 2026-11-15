package roll

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/controllers/filmstock"
	"meta-frames-server/internal/controllers/lens"
	"meta-frames-server/internal/controllers/processing"
	"meta-frames-server/internal/controllers/scan"
	"meta-frames-server/internal/db/gen"
	processingsvc "meta-frames-server/internal/services/processing"
	rollsvc "meta-frames-server/internal/services/roll"
	scannersvc "meta-frames-server/internal/services/scanner"
)

// Controller serves the roll endpoints.
type Controller struct {
	rolls    *rollsvc.Service
	scanners *scannersvc.Service
}

// New builds the controller around its services.
func New(rolls *rollsvc.Service, scanners *scannersvc.Service) *Controller {
	return &Controller{rolls: rolls, scanners: scanners}
}

func expiryInput(expiry *api.ExpiryMonth) (year, month *int) {
	if expiry == nil {
		return nil, nil
	}
	return &expiry.Year, expiry.Month
}

func ToAPIRollSummary(summary rollsvc.Summary) api.RollSummary {
	mapped := api.RollSummary{
		Id: summary.ID, FilmStockId: summary.FilmStockID, StockBrand: summary.StockBrand, StockName: summary.StockName,
		CameraId: summary.CameraID, Format: int(summary.Format), Exposures: int(summary.Exposures),
		Status: api.RollStatus(summary.Status), ShotIso: pointers.Int(summary.ShotIso), Price: pointers.Int(summary.Price),
		Description: summary.Description, StartedAt: api.ToAPIDate(summary.StartedAt), FinishedAt: api.ToAPIDate(summary.FinishedAt),
		NegativesAtLab: summary.NegativesAtLab, Developed: summary.Developed, Scanned: summary.Scanned,
		OpenJobTypes: make([]api.ProcessingType, len(summary.OpenJobTypes)), OpenAtLab: summary.OpenAtLab,
		Plan: api.RollPlan{
			Target:    (*api.RollPlanTarget)(summary.Target),
			DevelopBy: (*api.StepActor)(summary.DevelopBy),
			ScanBy:    (*api.StepActor)(summary.ScanBy),
		},
		Stage: api.RollSummaryStage(summary.Stage), StageDetail: (*api.RollSummaryStageDetail)(summary.StageDetail),
		NeedsScan: summary.ScanRequestedAt.Valid,
		Version:   summary.Version,
	}
	for index, jobType := range summary.OpenJobTypes {
		mapped.OpenJobTypes[index] = api.ProcessingType(jobType)
	}
	// the query gives uuid.Nil when the roll has no open handoff
	if summary.OpenHandoffID != uuid.Nil {
		mapped.OpenHandoffId = &summary.OpenHandoffID
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
		StartedFrom: api.FromAPIDate(params.StartedFrom), StartedTo: api.FromAPIDate(params.StartedTo),
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
	expiryYear, expiryMonth := expiryInput(body.Expiry)
	input := rollsvc.BulkInput{
		FilmStockID: body.FilmStockId, Format: body.Format, Exposures: body.Exposures, Quantity: body.Quantity,
		Price: body.Price, ExpiryYear: expiryYear, ExpiryMonth: expiryMonth,
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

func (controller *Controller) HandOverRolls(ctx context.Context, request api.HandOverRollsRequestObject) (api.HandOverRollsResponseObject, error) {
	body := request.Body
	input := rollsvc.HandOverInput{
		LabID: body.LabId, HandedAt: api.FromAPIDate(body.HandedAt), Notes: body.Notes,
		ResolveScanOrders: func(queries gen.Querier) ([]processingsvc.ScanOrderInput, error) {
			pick := func(ctx context.Context, scannerID *uuid.UUID, code *string) (uuid.UUID, error) {
				return controller.scanners.PickForOrderIn(ctx, queries, scannerID, code)
			}
			return processing.ScanOrderInputs(ctx, pick, body.ScanOrders)
		},
		Rolls: make([]rollsvc.HandOverRoll, len(body.Rolls)), IdempotencyKey: pointers.Value(request.Params.IdempotencyKey),
	}
	for index, item := range body.Rolls {
		input.Rolls[index] = rollsvc.HandOverRoll{RollID: item.RollId, Type: string(item.Type)}
	}
	summaries, err := controller.rolls.HandOver(ctx, input)
	if err != nil {
		return nil, err
	}
	return api.HandOverRolls201JSONResponse(toAPIRollSummaries(summaries)), nil
}

func (controller *Controller) ReturnLabHandoff(ctx context.Context, request api.ReturnLabHandoffRequestObject) (api.ReturnLabHandoffResponseObject, error) {
	summary, err := controller.rolls.ReturnHandoff(ctx, request.Id, api.OptionalDate(request.Body))
	if err != nil {
		return nil, err
	}
	return api.ReturnLabHandoff200JSONResponse(ToAPIRollSummary(summary)), nil
}

// parseIfMatch reads an If-Match header: `*`, or a comma-separated list of strong entity tags
// holding a roll version ("3"). Anything else, weak tags included, is refused so that a client
// that meant to send a precondition never overwrites unconditionally.
func parseIfMatch(header *string) (*rollsvc.VersionCondition, error) {
	if header == nil {
		return nil, nil
	}
	invalid := apperror.BadRequest("invalid_precondition", `If-Match must be "*" or quoted roll versions such as "3"`)
	value := strings.TrimSpace(*header)
	if value == "*" {
		return &rollsvc.VersionCondition{Any: true}, nil
	}
	condition := rollsvc.VersionCondition{}
	for _, tag := range strings.Split(value, ",") {
		digits, quoted := strings.CutPrefix(strings.TrimSpace(tag), `"`)
		digits, closed := strings.CutSuffix(digits, `"`)
		if !quoted || !closed || digits == "" || strings.Trim(digits, "0123456789") != "" {
			return nil, invalid
		}
		version, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return nil, invalid
		}
		condition.Versions = append(condition.Versions, version)
	}
	return &condition, nil
}

func (controller *Controller) PutRoll(ctx context.Context, request api.PutRollRequestObject) (api.PutRollResponseObject, error) {
	body := request.Body
	ifMatch, err := parseIfMatch(request.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	expiryYear, expiryMonth := expiryInput(body.Expiry)
	detail, err := controller.rolls.Update(ctx, request.Id, rollsvc.Input{
		FilmStockID: body.FilmStockId, Format: body.Format, Exposures: body.Exposures, Price: body.Price,
		ExpiryYear: expiryYear, ExpiryMonth: expiryMonth, ShotISO: body.ShotIso,
		StartedAt: api.FromAPIDate(body.StartedAt), FinishedAt: api.FromAPIDate(body.FinishedAt), Description: body.Description,
		IfMatch: ifMatch,
	})
	if err != nil {
		return nil, err
	}
	return api.PutRoll200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) PutRollNotes(ctx context.Context, request api.PutRollNotesRequestObject) (api.PutRollNotesResponseObject, error) {
	detail, err := controller.rolls.SetNotes(ctx, request.Id, request.Body.Description, request.Body.BaseDescription)
	if err != nil {
		return nil, err
	}
	return api.PutRollNotes200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) PutRollPlan(ctx context.Context, request api.PutRollPlanRequestObject) (api.PutRollPlanResponseObject, error) {
	body := request.Body
	detail, err := controller.rolls.SetPlan(ctx, request.Id, rollsvc.PlanInput{
		Target: (*string)(body.Target), DevelopBy: (*string)(body.DevelopBy), ScanBy: (*string)(body.ScanBy),
	})
	if err != nil {
		return nil, err
	}
	return api.PutRollPlan200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) MarkRollDeveloped(ctx context.Context, request api.MarkRollDevelopedRequestObject) (api.MarkRollDevelopedResponseObject, error) {
	input := rollsvc.DevelopedInput{}
	if body := request.Body; body != nil {
		input.DevelopedAt, input.Target = api.FromAPIDate(body.Date), (*string)(body.Target)
	}
	detail, err := controller.rolls.MarkDeveloped(ctx, request.Id, input)
	if err != nil {
		return nil, err
	}
	return api.MarkRollDeveloped200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) MarkRollForScan(ctx context.Context, request api.MarkRollForScanRequestObject) (api.MarkRollForScanResponseObject, error) {
	detail, err := controller.rolls.SetScanRequest(ctx, request.Id, true)
	if err != nil {
		return nil, err
	}
	return api.MarkRollForScan200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) UnmarkRollForScan(ctx context.Context, request api.UnmarkRollForScanRequestObject) (api.UnmarkRollForScanResponseObject, error) {
	detail, err := controller.rolls.SetScanRequest(ctx, request.Id, false)
	if err != nil {
		return nil, err
	}
	return api.UnmarkRollForScan200JSONResponse(toAPIRollDetail(detail)), nil
}

func (controller *Controller) LoadRoll(ctx context.Context, request api.LoadRollRequestObject) (api.LoadRollResponseObject, error) {
	body := request.Body
	detail, err := controller.rolls.Load(ctx, request.Id, rollsvc.LoadInput{
		CameraID: body.CameraId, StartedAt: api.FromAPIDate(body.StartedAt), ShotISO: body.ShotIso, LensIDs: body.LensIds,
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
	detail, err := controller.rolls.Finish(ctx, request.Id, api.OptionalDate(finishedAt))
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
		mapped.Expiring[index] = api.ExpiryRoll{Roll: ToAPIRollSummary(expiring.Roll), ExpiresOn: api.APIDate(expiring.ExpiresOn), Expired: expiring.Expired}
	}
	return api.GetExpiry200JSONResponse(mapped), nil
}

func (controller *Controller) DeleteRoll(ctx context.Context, request api.DeleteRollRequestObject) (api.DeleteRollResponseObject, error) {
	if err := controller.rolls.Delete(ctx, request.Id); err != nil {
		return nil, err
	}
	return api.DeleteRoll204Response{}, nil
}
