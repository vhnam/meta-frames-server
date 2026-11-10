package roll

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services/processing"
	"meta-frames-server/internal/services/shared"
)

// HandOverInput hands several rolls to one lab at once: one job per roll, one handoff per roll,
// all sharing a batch.
type HandOverInput struct {
	LabID    uuid.UUID
	HandedAt *time.Time
	Notes    *string
	// ResolveScanOrders runs inside the batch transaction, so a refused batch also drops a scanner it
	// created. The orders go to every job that produces scans; a develop job gets none.
	ResolveScanOrders func(queries gen.Querier) ([]processing.ScanOrderInput, error)
	Rolls             []HandOverRoll
	IdempotencyKey    string
}

type HandOverRoll struct {
	RollID uuid.UUID
	Type   string
}

// HandOver records rolls handed to a lab together (Giao lab). All or nothing: one roll that cannot
// go refuses the whole batch, and the error names it.
func (service *Service) HandOver(ctx context.Context, input HandOverInput) ([]Summary, error) {
	rollIDs := make([]uuid.UUID, len(input.Rolls))
	needsScanOrders := false
	for index, item := range input.Rolls {
		rollIDs[index] = item.RollID
		needsScanOrders = needsScanOrders || processing.JobProducesScans(item.Type)
	}
	if len(shared.RemoveDuplicates(rollIDs)) != len(rollIDs) {
		return nil, apperror.Unprocessable("duplicate_roll", "each roll can appear only once in a handoff")
	}
	var handed []Summary
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		owner := requestctx.Owner(ctx)
		if input.IdempotencyKey != "" {
			claimed, err := queries.ClaimIdempotencyKey(ctx, gen.ClaimIdempotencyKeyParams{Key: input.IdempotencyKey, OwnerID: owner})
			if err != nil {
				return err
			}
			if claimed == 0 {
				handed, err = replayBulkRolls(ctx, queries, input.IdempotencyKey)
				return err
			}
		}
		// A batch with no scan job ignores scanOrders, so "other" creates no scanner it would not use.
		var scanOrders []processing.ScanOrderInput
		var err error
		if needsScanOrders {
			if scanOrders, err = input.ResolveScanOrders(queries); err != nil {
				return err
			}
			if len(scanOrders) == 0 {
				return apperror.Unprocessable("scan_orders_required", "scanOrders needs at least one scanner when a roll is to be scanned")
			}
		}
		if _, err := queries.GetLab(ctx, gen.GetLabParams{ID: input.LabID, OwnerID: owner}); err != nil {
			if db.IsNoRows(err) {
				return apperror.Unprocessable("unknown_lab", "lab does not exist")
			}
			return err
		}

		// Lock in one order, so two batches sharing rolls cannot deadlock.
		locked := slices.Clone(input.Rolls)
		slices.SortFunc(locked, func(left, right HandOverRoll) int { return bytes.Compare(left.RollID[:], right.RollID[:]) })
		rolls := make(map[uuid.UUID]gen.Roll, len(locked))
		for _, item := range locked {
			roll, err := queries.GetRollForUpdate(ctx, gen.GetRollForUpdateParams{ID: item.RollID, OwnerID: owner})
			if err != nil {
				return forRoll(item.RollID, shared.NotFoundOr(err, "roll"))
			}
			rolls[item.RollID] = roll
		}

		handedAt := shared.DateOrDefault(input.HandedAt, service.clock.Today())
		batchID := service.newID()
		for _, item := range input.Rolls {
			summary, err := getRollSummary(ctx, queries, item.RollID)
			if err != nil {
				return err
			}
			// shortcut: until slice 3b no request closes a handoff, so an open one blocks the roll.
			open, err := queries.RollHasOpenHandoff(ctx, gen.RollHasOpenHandoffParams{RollID: item.RollID, OwnerID: owner})
			if err != nil {
				return err
			}
			if open {
				return forRoll(item.RollID, apperror.Conflict("negatives_at_lab", "a lab still holds this roll; record its return first"))
			}
			if err := checkCanHandOver(summary, item.Type); err != nil {
				return forRoll(item.RollID, err)
			}
			handoffID := service.newID()
			if err := queries.InsertLabHandoff(ctx, gen.InsertLabHandoffParams{
				ID: handoffID, OwnerID: owner, RollID: item.RollID, LabID: input.LabID, BatchID: batchID,
				HandedAt: handedAt, Notes: pointers.TrimmedOrNil(input.Notes),
			}); err != nil {
				return err
			}
			job := processing.Input{LabID: &input.LabID, Type: item.Type}
			if processing.JobProducesScans(item.Type) {
				job.ScanOrders = scanOrders
			}
			if err := processing.CreateJob(ctx, queries, rolls[item.RollID], service.newID(), job, handedAt, &handoffID); err != nil {
				return forRoll(item.RollID, err)
			}
			if processing.JobProducesScans(item.Type) {
				if err := queries.ClearRollScanRequest(ctx, gen.ClearRollScanRequestParams{ID: item.RollID, OwnerID: owner}); err != nil {
					return err
				}
			}
		}

		if handed, err = summariesByID(ctx, queries, rollIDs); err != nil {
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
			Key: input.IdempotencyKey, Status: pointers.To(int32(201)), Response: json.RawMessage(stored), OwnerID: owner,
		})
	})
	return handed, err
}

// checkCanHandOver decides whether a roll waits to go to a lab, and for which job: a finished roll
// to be developed, a developed roll waiting for its planned scan, or a completed roll marked for a
// scan. The negatives must be home: a roll a lab still holds waits for the same-lab rule (slice 3c).
func checkCanHandOver(summary Summary, jobType string) error {
	if summary.NegativesAtLab {
		return apperror.Conflict("negatives_at_lab", "a lab still holds this roll; record its return first")
	}
	waitingForScan := summary.Stage == stageCompleted && summary.ScanRequestedAt.Valid
	if summary.Stage == stageProcessing && summary.StageDetail != nil {
		switch *summary.StageDetail {
		case detailWaitingScan, detailWaitingHomeScan, detailWaitingLabScan:
			waitingForScan = true
		}
	}
	switch {
	case summary.Stage == domain.RollStatusDoneShooting:
		if jobType != domain.JobTypeDevelop && jobType != domain.JobTypeDevelopScan {
			return apperror.Unprocessable("invalid_job_type", "a roll that is done shooting goes for develop or develop_scan")
		}
	case waitingForScan:
		if jobType != domain.JobTypeScan {
			return apperror.Unprocessable("invalid_job_type", "a developed roll goes for scan")
		}
	default:
		return apperror.Conflict("roll_not_waiting", "the roll is not waiting to go to a lab")
	}
	return nil
}

// forRoll prefixes a business error with the roll it is about, so a refused batch says which roll.
func forRoll(rollID uuid.UUID, err error) error {
	appError, ok := apperror.From(err)
	if !ok {
		return err
	}
	return &apperror.Error{Kind: appError.Kind, Code: appError.Code, Message: "roll " + rollID.String() + ": " + appError.Message}
}
