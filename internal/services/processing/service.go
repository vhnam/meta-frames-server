package processing

import (
	"context"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/domain"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
	clock clock.Clock
}

func New(store db.Store, appClock clock.Clock) *Service {
	return &Service{store: store, clock: appClock}
}

// ---- pure rules ----

// isJobOpen: a job stays open until its expected result is received (section 1.3).
func isJobOpen(job gen.Processing) bool {
	switch job.Type {
	case domain.JobTypeDevelopScan, domain.JobTypeScan:
		return job.ScansReceivedAt == nil
	default:
		return job.NegativesReturnedAt == nil
	}
}

// statusAfterJobs is the roll status once its jobs are known: at_lab while any job is open,
// otherwise scanned if scans were ever received, else developed.
func statusAfterJobs(hasOpenJob, hasReceivedScans bool) string {
	switch {
	case hasOpenJob:
		return domain.RollStatusAtLab
	case hasReceivedScans:
		return domain.RollStatusScanned
	default:
		return domain.RollStatusDeveloped
	}
}

// checkCanSend decides whether a roll in rollStatus may be sent for a job of jobType (UC-24, UC-28).
func checkCanSend(rollStatus, jobType string) error {
	switch rollStatus {
	case domain.RollStatusDoneShooting:
		return nil
	case domain.RollStatusScanned, domain.RollStatusDeveloped:
		if jobType == domain.JobTypeScan || jobType == domain.JobTypePrint {
			return nil
		}
		return apperror.Conflict("invalid_job_type", "a processed roll can only be sent again for scan or print")
	default:
		return apperror.Conflict("roll_not_ready", "roll must be done shooting before it is sent for processing")
	}
}

func JobProducesScans(jobType string) bool {
	return jobType == domain.JobTypeDevelopScan || jobType == domain.JobTypeScan
}

// ---- shared queries ----

func BuildViews(ctx context.Context, queries gen.Querier, rollID uuid.UUID) ([]View, error) {
	rows, err := queries.ListRollProcessing(ctx, rollID)
	if err != nil {
		return nil, err
	}
	views := make([]View, len(rows))
	for index, row := range rows {
		job := gen.Processing{
			ID: row.ID, RollID: row.RollID, LabID: row.LabID, Type: row.Type, Process: row.Process, SentAt: row.SentAt,
			ScansReceivedAt: row.ScansReceivedAt, NegativesReturnedAt: row.NegativesReturnedAt, Price: row.Price, Notes: row.Notes,
		}
		views[index] = View{Job: job, LabName: row.LabName, Scanners: row.Scanners, IsOpen: isJobOpen(job)}
	}
	return views, nil
}

func BuildView(ctx context.Context, queries gen.Querier, jobID uuid.UUID) (View, error) {
	job, err := queries.GetProcessing(ctx, jobID)
	if err != nil {
		return View{}, shared.NotFoundOr(err, "processing job")
	}
	views, err := BuildViews(ctx, queries, job.RollID)
	if err != nil {
		return View{}, err
	}
	for _, view := range views {
		if view.Job.ID == jobID {
			return view, nil
		}
	}
	return View{}, apperror.NotFound("processing job")
}

// RefreshRollStatus applies the lifecycle rule of section 1.3 after a job changes.
func RefreshRollStatus(ctx context.Context, queries gen.Querier, rollID uuid.UUID) error {
	roll, err := queries.GetRoll(ctx, rollID)
	if err != nil {
		return err
	}
	switch roll.Status {
	case domain.RollStatusAtLab, domain.RollStatusScanned, domain.RollStatusDeveloped:
	default:
		return nil
	}
	hasOpenJob, err := queries.RollHasOpenJob(ctx, rollID)
	if err != nil {
		return err
	}
	hasReceivedScans := false
	if !hasOpenJob {
		if hasReceivedScans, err = queries.RollHasScansReceived(ctx, rollID); err != nil {
			return err
		}
	}
	next := statusAfterJobs(hasOpenJob, hasReceivedScans)
	if next == roll.Status {
		return nil
	}
	return queries.SetRollStatus(ctx, gen.SetRollStatusParams{ID: rollID, Status: next})
}

// ---- use cases ----

func (service *Service) ListForRoll(ctx context.Context, rollID uuid.UUID) ([]View, error) {
	queries := service.store.Queries()
	if _, err := queries.GetRoll(ctx, rollID); err != nil {
		return nil, shared.NotFoundOr(err, "roll")
	}
	return BuildViews(ctx, queries, rollID)
}

func (service *Service) Get(ctx context.Context, jobID uuid.UUID) (View, error) {
	return BuildView(ctx, service.store.Queries(), jobID)
}

// Save sends a roll for processing or edits an existing job (UC-24, UC-28).
// The boolean reports that a new job was created.
func (service *Service) Save(ctx context.Context, rollID, jobID uuid.UUID, input Input) (View, bool, error) {
	var (
		view    View
		created bool
	)
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		roll, err := queries.GetRollForUpdate(ctx, rollID)
		if err != nil {
			return shared.NotFoundOr(err, "roll")
		}
		if input.LabID != nil {
			if _, err := queries.GetLab(ctx, *input.LabID); err != nil {
				if db.IsNoRows(err) {
					return apperror.Unprocessable("unknown_lab", "lab does not exist")
				}
				return err
			}
		}
		sentAt := shared.DateOrDefault(input.SentAt, service.clock.Today())

		existing, lookupErr := queries.GetProcessingForUpdate(ctx, jobID)
		if lookupErr != nil && !db.IsNoRows(lookupErr) {
			return lookupErr
		}
		if lookupErr == nil {
			if existing.RollID != rollID || existing.Type != input.Type {
				return apperror.Conflict("job_immutable", "a job's roll and type cannot change")
			}
			if _, err := queries.UpdateProcessing(ctx, gen.UpdateProcessingParams{
				ID: jobID, LabID: input.LabID, SentAt: sentAt, Price: pointers.Int32(input.Price), Notes: pointers.TrimmedOrNil(input.Notes),
			}); err != nil {
				return err
			}
		} else {
			created = true
			if err := checkCanSend(roll.Status, input.Type); err != nil {
				return err
			}
			process, err := resolveProcess(ctx, queries, roll.FilmStockID, input.Process)
			if err != nil {
				return err
			}
			if _, err := queries.InsertProcessing(ctx, gen.InsertProcessingParams{
				ID: jobID, RollID: rollID, LabID: input.LabID, Type: input.Type, Process: process,
				SentAt: sentAt, Price: pointers.Int32(input.Price), Notes: pointers.TrimmedOrNil(input.Notes),
			}); err != nil {
				return err
			}
			if err := queries.SetRollStatus(ctx, gen.SetRollStatusParams{ID: rollID, Status: domain.RollStatusAtLab}); err != nil {
				return err
			}
		}
		view, err = BuildView(ctx, queries, jobID)
		return err
	})
	return view, created, err
}

// resolveProcess uses the requested process, defaulting to the film stock's own (UC-24 step 4).
func resolveProcess(ctx context.Context, queries gen.Querier, stockID uuid.UUID, requested *string) (string, error) {
	if requested != nil {
		return *requested, nil
	}
	stock, err := queries.GetFilmStock(ctx, stockID)
	if err != nil {
		return "", err
	}
	return stock.Process, nil
}

// RecordScansReceived marks scans as arrived (UC-25).
func (service *Service) RecordScansReceived(ctx context.Context, jobID uuid.UUID, receivedAt *time.Time) (View, error) {
	var view View
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		job, err := queries.GetProcessingForUpdate(ctx, jobID)
		if err != nil {
			return shared.NotFoundOr(err, "processing job")
		}
		if !JobProducesScans(job.Type) {
			return apperror.Conflict("no_scans_expected", "this job type does not produce scans")
		}
		if _, err := queries.GetRollForUpdate(ctx, job.RollID); err != nil {
			return err
		}
		date := shared.DateOrDefault(receivedAt, service.clock.Today())
		if _, err := queries.SetScansReceived(ctx, gen.SetScansReceivedParams{ID: jobID, ScansReceivedAt: &date}); err != nil {
			return err
		}
		if err := RefreshRollStatus(ctx, queries, job.RollID); err != nil {
			return err
		}
		view, err = BuildView(ctx, queries, jobID)
		return err
	})
	return view, err
}

// RecordNegativesReturned marks negatives as back from the lab (UC-26).
func (service *Service) RecordNegativesReturned(ctx context.Context, jobID uuid.UUID, returnedAt *time.Time) (View, error) {
	var view View
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		job, err := queries.GetProcessingForUpdate(ctx, jobID)
		if err != nil {
			return shared.NotFoundOr(err, "processing job")
		}
		if job.LabID == nil {
			return apperror.Conflict("home_processing", "negatives of home-processed rolls do not need to be returned")
		}
		if _, err := queries.GetRollForUpdate(ctx, job.RollID); err != nil {
			return err
		}
		date := shared.DateOrDefault(returnedAt, service.clock.Today())
		if _, err := queries.SetNegativesReturned(ctx, gen.SetNegativesReturnedParams{ID: jobID, NegativesReturnedAt: &date}); err != nil {
			return err
		}
		if err := RefreshRollStatus(ctx, queries, job.RollID); err != nil {
			return err
		}
		view, err = BuildView(ctx, queries, jobID)
		return err
	})
	return view, err
}

// NegativesAtLab lists lab jobs whose negatives are not back yet (UC-27).
func (service *Service) NegativesAtLab(ctx context.Context) ([]gen.NegativesAtLabRow, error) {
	return service.store.Queries().NegativesAtLab(ctx)
}

// Delete soft-deletes a job that has no scans, then recomputes the roll's status (UC-28).
func (service *Service) Delete(ctx context.Context, jobID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		job, err := queries.GetProcessingForUpdate(ctx, jobID)
		if err != nil {
			return shared.NotFoundOr(err, "processing job")
		}
		hasScans, err := queries.ProcessingHasScans(ctx, jobID)
		if err != nil {
			return err
		}
		if hasScans {
			return apperror.Conflict("job_has_scans", "a job with scans cannot be deleted; delete its scans first")
		}
		if _, err := queries.GetRollForUpdate(ctx, job.RollID); err != nil {
			return err
		}
		if _, err := queries.SoftDeleteProcessing(ctx, jobID); err != nil {
			return err
		}
		remaining, err := queries.ListRollProcessing(ctx, job.RollID)
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			return RefreshRollStatus(ctx, queries, job.RollID)
		}
		// No job left: the roll is back to "done shooting".
		roll, err := queries.GetRoll(ctx, job.RollID)
		if err != nil {
			return err
		}
		switch roll.Status {
		case domain.RollStatusAtLab, domain.RollStatusDeveloped, domain.RollStatusScanned:
			return queries.SetRollStatus(ctx, gen.SetRollStatusParams{ID: job.RollID, Status: domain.RollStatusDoneShooting})
		}
		return nil
	})
}
