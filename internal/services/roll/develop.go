package roll

import (
	"context"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/processing"
	"meta-frames-server/internal/services/shared"
)

// DevelopedInput is "Developed" (D4).
type DevelopedInput struct {
	DevelopedAt *time.Time
	// Target, when given, sets the plan's target. Develop only clears scanBy, which needs a scan.
	Target *string
}

// MarkDeveloped records the day a roll's film was developed, and the target chosen then.
func (service *Service) MarkDeveloped(ctx context.Context, rollID uuid.UUID, input DevelopedInput) (Detail, error) {
	var detail Detail
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		owner := requestctx.Owner(ctx)
		roll, err := queries.GetRollForUpdate(ctx, gen.GetRollForUpdateParams{ID: rollID, OwnerID: owner})
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		if err := processing.RecordDeveloped(ctx, queries, roll, shared.DateOrDefault(input.DevelopedAt, service.clock.Today())); err != nil {
			return err
		}
		if input.Target != nil {
			scanBy := roll.ScanBy
			if *input.Target == targetDeveloped {
				scanBy = nil
			}
			if _, err := queries.SetRollPlan(ctx, gen.SetRollPlanParams{
				ID: rollID, Target: input.Target, DevelopBy: roll.DevelopBy, ScanBy: scanBy, OwnerID: owner,
			}); err != nil {
				return err
			}
		}
		detail, err = buildRollDetail(ctx, queries, service.clock.Today(), rollID, nil)
		return err
	})
	return detail, err
}

// SetScanRequest marks a roll "Needs scan" (D3), or removes the mark. Allowed wherever the roll is;
// marking again keeps the first date.
func (service *Service) SetScanRequest(ctx context.Context, rollID uuid.UUID, requested bool) (Detail, error) {
	var detail Detail
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		owner := requestctx.Owner(ctx)
		if _, err := queries.GetRollForUpdate(ctx, gen.GetRollForUpdateParams{ID: rollID, OwnerID: owner}); err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		var err error
		if requested {
			err = queries.SetRollScanRequest(ctx, gen.SetRollScanRequestParams{ID: rollID, OwnerID: owner})
		} else {
			err = queries.ClearRollScanRequest(ctx, gen.ClearRollScanRequestParams{ID: rollID, OwnerID: owner})
		}
		if err != nil {
			return err
		}
		detail, err = buildRollDetail(ctx, queries, service.clock.Today(), rollID, nil)
		return err
	})
	return detail, err
}
