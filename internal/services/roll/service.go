package roll

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services/processing"
	"meta-frames-server/internal/services/scan"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
	clock clock.Clock
	newID func() uuid.UUID
}

func New(store db.Store, appClock clock.Clock) *Service {
	return &Service{store: store, clock: appClock, newID: uuid.New}
}

// ---- pure rules ----

func validateExpiry(year, month *int) error {
	if month != nil && year == nil {
		return apperror.Unprocessable("expiry_year_required", "expiry month requires a year")
	}
	if month != nil && (*month < 1 || *month > 12) {
		return apperror.Unprocessable("invalid_expiry_month", "expiry month must be 1-12")
	}
	return nil
}

// resolveExposures defaults to 36 for 135 film and otherwise requires an explicit value (UC-15).
func resolveExposures(format int, exposures *int) (int, error) {
	if exposures != nil {
		return *exposures, nil
	}
	if format == format135 {
		return defaultExposures135, nil
	}
	return 0, apperror.Unprocessable("exposures_required", "exposures are required for formats other than 135")
}

// normalizeShotISO stores null when a roll is shot at box ISO.
func normalizeShotISO(shotISO *int, boxISO int32) *int32 {
	converted := pointers.Int32(shotISO)
	if converted != nil && *converted == boxISO {
		return nil
	}
	return converted
}

func filterToParams(filter Filter) gen.ListRollSummariesParams {
	return gen.ListRollSummariesParams{
		Status: filter.Status, FilmStockID: filter.FilmStockID, CameraID: filter.CameraID, LensID: filter.LensID,
		Format: pointers.Int32(filter.Format), StartedFrom: filter.StartedFrom, StartedTo: filter.StartedTo,
	}
}

// ---- shared queries ----

func ListSummaries(ctx context.Context, queries gen.Querier, params gen.ListRollSummariesParams) ([]Summary, error) {
	rows, err := queries.ListRollSummaries(ctx, params)
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, len(rows))
	for index, row := range rows {
		summaries[index] = Summary{row}
	}
	return summaries, nil
}

func getRollSummary(ctx context.Context, queries gen.Querier, rollID uuid.UUID) (Summary, error) {
	summaries, err := ListSummaries(ctx, queries, gen.ListRollSummariesParams{RollID: &rollID})
	if err != nil {
		return Summary{}, err
	}
	if len(summaries) == 0 {
		return Summary{}, apperror.NotFound("roll")
	}
	return summaries[0], nil
}

// buildRollDetail assembles everything about one roll (UC-22).
func buildRollDetail(ctx context.Context, queries gen.Querier, today time.Time, rollID uuid.UUID, warnings []string) (Detail, error) {
	summary, err := getRollSummary(ctx, queries, rollID)
	if err != nil {
		return Detail{}, err
	}
	stock, err := queries.GetFilmStock(ctx, summary.FilmStockID)
	if err != nil {
		return Detail{}, err
	}
	detail := Detail{Summary: summary, Stock: stock, Warnings: append([]string{}, warnings...)}
	if stock.BaseStockID != nil {
		base, err := queries.GetFilmStock(ctx, *stock.BaseStockID)
		if err != nil {
			return detail, err
		}
		detail.BaseStock = &base
	}
	if summary.Status == domain.RollStatusInStock && isExpired(summary.ExpiryYear, summary.ExpiryMonth, today) {
		detail.Warnings = append(detail.Warnings, "roll is expired")
	}

	if detail.Lenses, err = queries.ListRollLenses(ctx, rollID); err != nil {
		return detail, err
	}
	if detail.Processing, err = processing.BuildViews(ctx, queries, rollID); err != nil {
		return detail, err
	}
	if detail.Frames, err = scan.BuildFrameViews(ctx, queries, rollID); err != nil {
		return detail, err
	}
	spend, err := queries.RollSpend(ctx, rollID)
	if err != nil {
		return detail, err
	}
	detail.Totals = Totals{
		RollPrice: spend.RollPrice, ProcessingPrice: spend.ProcessingPrice,
		Incomplete: spend.RollPriceMissing || spend.ProcessingPriceMissing,
	}
	return detail, nil
}

// ---- use cases ----

func (service *Service) List(ctx context.Context, filter Filter) ([]Summary, error) {
	return ListSummaries(ctx, service.store.Queries(), filterToParams(filter))
}

func (service *Service) Detail(ctx context.Context, rollID uuid.UUID) (Detail, error) {
	return buildRollDetail(ctx, service.store.Queries(), service.clock.Today(), rollID, nil)
}

// AddBulk creates one record per physical roll (UC-15). With an idempotency key, a retry
// returns the rolls of the first attempt instead of creating more.
func (service *Service) AddBulk(ctx context.Context, input BulkInput) ([]Summary, error) {
	if err := validateExpiry(input.ExpiryYear, input.ExpiryMonth); err != nil {
		return nil, err
	}
	exposures, err := resolveExposures(input.Format, input.Exposures)
	if err != nil {
		return nil, err
	}

	var created []Summary
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if input.IdempotencyKey != "" {
			claimed, err := queries.ClaimIdempotencyKey(ctx, input.IdempotencyKey)
			if err != nil {
				return err
			}
			if claimed == 0 {
				created, err = replayBulkRolls(ctx, queries, input.IdempotencyKey)
				return err
			}
		}
		if _, err := queries.GetFilmStock(ctx, input.FilmStockID); err != nil {
			if db.IsNoRows(err) {
				return apperror.Unprocessable("unknown_film_stock", "film stock does not exist")
			}
			return err
		}

		rollIDs := make([]uuid.UUID, input.Quantity)
		for index := range rollIDs {
			rollIDs[index] = service.newID()
			if _, err := queries.InsertRoll(ctx, gen.InsertRollParams{
				ID: rollIDs[index], FilmStockID: input.FilmStockID, Format: int32(input.Format), Exposures: int32(exposures),
				Price: pointers.Int32(input.Price), ExpiryYear: pointers.Int32(input.ExpiryYear), ExpiryMonth: pointers.Int32(input.ExpiryMonth),
			}); err != nil {
				return err
			}
		}
		created, err = summariesByID(ctx, queries, rollIDs)
		if err != nil {
			return err
		}
		if input.IdempotencyKey == "" {
			return nil
		}
		stored, err := json.Marshal(rollIDs)
		if err != nil {
			return err
		}
		return queries.FinishIdempotencyKey(ctx, gen.FinishIdempotencyKeyParams{
			Key: input.IdempotencyKey, Status: pointers.To(int32(201)), Response: json.RawMessage(stored),
		})
	})
	return created, err
}

func summariesByID(ctx context.Context, queries gen.Querier, rollIDs []uuid.UUID) ([]Summary, error) {
	summaries := make([]Summary, 0, len(rollIDs))
	for _, rollID := range rollIDs {
		summary, err := getRollSummary(ctx, queries, rollID)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// replayBulkRolls returns the rolls created by an earlier request with the same key.
func replayBulkRolls(ctx context.Context, queries gen.Querier, key string) ([]Summary, error) {
	stored, err := queries.GetIdempotencyKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if stored.Response == nil {
		return nil, apperror.Conflict("request_in_progress", "a request with this Idempotency-Key is still running")
	}
	var rollIDs []uuid.UUID
	if err := json.Unmarshal([]byte(stored.Response), &rollIDs); err != nil {
		return nil, err
	}
	return summariesByID(ctx, queries, rollIDs)
}

// Update edits roll details (UC-16).
func (service *Service) Update(ctx context.Context, rollID uuid.UUID, input Input) (Detail, error) {
	if err := validateExpiry(input.ExpiryYear, input.ExpiryMonth); err != nil {
		return Detail{}, err
	}
	if input.StartedAt != nil && input.FinishedAt != nil && input.FinishedAt.Before(*input.StartedAt) {
		return Detail{}, apperror.Unprocessable("invalid_dates", "finish date is before start date")
	}

	var detail Detail
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		roll, err := queries.GetRollForUpdate(ctx, rollID)
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		if input.FilmStockID != roll.FilmStockID && roll.Status != domain.RollStatusInStock {
			return apperror.Conflict("stock_locked", "film stock can only change while the roll is in stock")
		}
		stock, err := queries.GetFilmStock(ctx, input.FilmStockID)
		if db.IsNoRows(err) {
			return apperror.Unprocessable("unknown_film_stock", "film stock does not exist")
		}
		if err != nil {
			return err
		}
		if _, err := queries.UpdateRoll(ctx, gen.UpdateRollParams{
			ID: rollID, FilmStockID: input.FilmStockID, Format: int32(input.Format), Exposures: int32(input.Exposures),
			Price: pointers.Int32(input.Price), ExpiryYear: pointers.Int32(input.ExpiryYear), ExpiryMonth: pointers.Int32(input.ExpiryMonth),
			ShotIso: normalizeShotISO(input.ShotISO, stock.BoxIso), StartedAt: input.StartedAt, FinishedAt: input.FinishedAt,
			Description: pointers.TrimmedOrNil(input.Description),
		}); err != nil {
			return err
		}
		detail, err = buildRollDetail(ctx, queries, service.clock.Today(), rollID, nil)
		return err
	})
	return detail, err
}

// Load puts an in-stock roll into a camera (UC-17). Repeating the same load changes nothing.
func (service *Service) Load(ctx context.Context, rollID uuid.UUID, input LoadInput) (Detail, error) {
	var detail Detail
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		roll, err := queries.GetRollForUpdate(ctx, rollID)
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		today := service.clock.Today()
		if roll.Status == domain.RollStatusInCamera && roll.CameraID != nil && *roll.CameraID == input.CameraID {
			detail, err = buildRollDetail(ctx, queries, today, rollID, nil)
			return err
		}
		if roll.Status != domain.RollStatusInStock {
			return apperror.Conflict("roll_not_in_stock", "only in-stock rolls can be loaded")
		}
		camera, err := queries.GetCameraForUpdate(ctx, input.CameraID)
		if db.IsNoRows(err) {
			return apperror.Unprocessable("unknown_camera", "camera does not exist")
		}
		if err != nil {
			return err
		}
		if !camera.IsActive {
			return apperror.Conflict("camera_inactive", "camera is inactive")
		}
		loaded, err := queries.CameraIsLoaded(ctx, &camera.ID)
		if err != nil {
			return err
		}
		if loaded {
			return apperror.Conflict("camera_loaded", "camera already holds a roll; finish it first")
		}
		stock, err := queries.GetFilmStock(ctx, roll.FilmStockID)
		if err != nil {
			return err
		}
		lensIDs, err := lensesForLoad(ctx, queries, camera, input.LensIDs)
		if err != nil {
			return err
		}

		var warnings []string
		if isExpired(roll.ExpiryYear, roll.ExpiryMonth, today) {
			warnings = append(warnings, "roll is expired")
		}
		if _, err := queries.LoadRoll(ctx, gen.LoadRollParams{
			ID: rollID, CameraID: &camera.ID, StartedAt: pointers.To(shared.DateOrDefault(input.StartedAt, today)),
			ShotIso: normalizeShotISO(input.ShotISO, stock.BoxIso),
		}); err != nil {
			return err
		}
		for _, lensID := range lensIDs {
			if err := queries.InsertRollLens(ctx, gen.InsertRollLensParams{RollID: rollID, LensID: lensID}); err != nil {
				return err
			}
		}
		detail, err = buildRollDetail(ctx, queries, today, rollID, warnings)
		return err
	})
	if db.IsUniqueViolation(err) {
		return Detail{}, apperror.Conflict("camera_loaded", "camera already holds a roll; finish it first")
	}
	return detail, err
}

// lensesForLoad decides which lenses go on a roll being loaded: the built-in lens of a
// fixed-lens camera, or the lenses the photographer picked (all optional).
func lensesForLoad(ctx context.Context, queries gen.Querier, camera gen.Camera, requested *[]uuid.UUID) ([]uuid.UUID, error) {
	if camera.HasFixedLens {
		linked, err := queries.ListCameraLenses(ctx, camera.ID)
		if err != nil {
			return nil, err
		}
		if len(linked) != 1 {
			return nil, apperror.Conflict("fixed_lens_data_error", "fixed-lens camera has no linked lens; fix the camera first")
		}
		return []uuid.UUID{linked[0].ID}, nil
	}
	if requested == nil {
		return nil, nil
	}
	lensIDs := shared.RemoveDuplicates(*requested)
	found, err := queries.GetLensesByIDs(ctx, lensIDs)
	if err != nil {
		return nil, err
	}
	if len(found) != len(lensIDs) {
		return nil, apperror.Unprocessable("unknown_lens", "one or more lenses do not exist")
	}
	for _, lens := range found {
		if !lens.IsActive {
			return nil, apperror.Unprocessable("inactive_lens", "inactive lenses cannot be used")
		}
	}
	return lensIDs, nil
}

// SetLenses replaces the lenses used on a roll (UC-18).
func (service *Service) SetLenses(ctx context.Context, rollID uuid.UUID, lensIDs []uuid.UUID) ([]gen.Lens, error) {
	lensIDs = shared.RemoveDuplicates(lensIDs)
	var lenses []gen.Lens
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		roll, err := queries.GetRollForUpdate(ctx, rollID)
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		if roll.CameraID == nil {
			return apperror.Conflict("roll_not_loaded", "roll has not been loaded into a camera")
		}
		camera, err := queries.GetCamera(ctx, *roll.CameraID)
		if err != nil {
			return err
		}
		if camera.HasFixedLens {
			return apperror.Conflict("fixed_lens_camera", "lenses of a fixed-lens camera cannot be changed")
		}
		current, err := queries.ListRollLenses(ctx, rollID)
		if err != nil {
			return err
		}
		requested, err := queries.GetLensesByIDs(ctx, lensIDs)
		if err != nil {
			return err
		}
		if err := checkUsableLenses(requested, lensIDs, shared.LensIDSet(current)); err != nil {
			return err
		}
		if err := queries.DeleteRollLensesExcept(ctx, gen.DeleteRollLensesExceptParams{RollID: rollID, Column2: lensIDs}); err != nil {
			return err
		}
		for _, lensID := range lensIDs {
			if err := queries.InsertRollLens(ctx, gen.InsertRollLensParams{RollID: rollID, LensID: lensID}); err != nil {
				return err
			}
		}
		lenses, err = queries.ListRollLenses(ctx, rollID)
		return err
	})
	return lenses, err
}

// checkUsableLenses enforces the UC-18 rules on the requested lens set.
func checkUsableLenses(found []gen.Lens, requestedIDs []uuid.UUID, alreadyOnRoll map[uuid.UUID]bool) error {
	if len(found) != len(requestedIDs) {
		return apperror.Unprocessable("unknown_lens", "one or more lenses do not exist")
	}
	for _, lens := range found {
		if !lens.IsActive && !alreadyOnRoll[lens.ID] {
			return apperror.Unprocessable("inactive_lens", "an inactive lens cannot be newly added")
		}
	}
	return nil
}

// Finish marks a roll as fully exposed and frees its camera (UC-19).
func (service *Service) Finish(ctx context.Context, rollID uuid.UUID, finishedAt *time.Time) (Detail, error) {
	var detail Detail
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		roll, err := queries.GetRollForUpdate(ctx, rollID)
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		if roll.Status != domain.RollStatusInCamera {
			return apperror.Conflict("roll_not_in_camera", "only a roll in a camera can be finished")
		}
		today := service.clock.Today()
		if _, err := queries.FinishRoll(ctx, gen.FinishRollParams{ID: rollID, FinishedAt: pointers.To(shared.DateOrDefault(finishedAt, today))}); err != nil {
			return err
		}
		detail, err = buildRollDetail(ctx, queries, today, rollID, nil)
		return err
	})
	return detail, err
}

// ExpiryReport lists in-stock rolls expired or expiring within 6 months (UC-21).
func (service *Service) ExpiryReport(ctx context.Context) (ExpiryReport, error) {
	rolls, err := ListSummaries(ctx, service.store.Queries(), gen.ListRollSummariesParams{Status: pointers.To(domain.RollStatusInStock)})
	if err != nil {
		return ExpiryReport{}, err
	}
	return buildExpiryReport(rolls, service.clock.Today()), nil
}

// Delete soft-deletes a roll that is still in stock, such as one entered by mistake.
func (service *Service) Delete(ctx context.Context, rollID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		roll, err := queries.GetRollForUpdate(ctx, rollID)
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		if roll.Status != domain.RollStatusInStock {
			return apperror.Conflict("roll_not_in_stock", "only a roll that is still in stock can be deleted")
		}
		_, err = queries.SoftDeleteRoll(ctx, rollID)
		return err
	})
}
