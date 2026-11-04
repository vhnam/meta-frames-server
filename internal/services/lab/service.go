package lab

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
}

func New(store db.Store) *Service { return &Service{store: store} }

// labScanners holds a lab's scanners in order, and its default scanner if it has one.
type labScanners struct {
	scannerIDs []uuid.UUID
	defaultID  *uuid.UUID
}

// scannersByLab groups the account's lab scanners by lab, each lab's in order.
func scannersByLab(ctx context.Context, queries gen.Querier) (map[uuid.UUID]labScanners, error) {
	rows, err := queries.ListLabScanners(ctx, requestctx.Owner(ctx))
	if err != nil {
		return nil, err
	}
	byLab := map[uuid.UUID]labScanners{}
	for _, row := range rows {
		scanners := byLab[row.LabID]
		scanners.scannerIDs = append(scanners.scannerIDs, row.ScannerID)
		if row.IsDefault {
			scanners.defaultID = &row.ScannerID
		}
		byLab[row.LabID] = scanners
	}
	return byLab, nil
}

func viewOf(lab gen.Lab, byLab map[uuid.UUID]labScanners) View {
	scanners := byLab[lab.ID]
	scannerIDs := scanners.scannerIDs
	if scannerIDs == nil {
		scannerIDs = []uuid.UUID{}
	}
	return View{Lab: lab, ScannerIDs: scannerIDs, DefaultScannerID: scanners.defaultID}
}

func (service *Service) List(ctx context.Context) ([]View, error) {
	queries := service.store.Queries()
	labs, err := queries.ListLabs(ctx, requestctx.Owner(ctx))
	if err != nil {
		return nil, err
	}
	byLab, err := scannersByLab(ctx, queries)
	if err != nil {
		return nil, err
	}
	views := make([]View, len(labs))
	for index, lab := range labs {
		views[index] = viewOf(lab, byLab)
	}
	return views, nil
}

func (service *Service) Get(ctx context.Context, labID uuid.UUID) (View, error) {
	return getView(ctx, service.store.Queries(), labID)
}

func getView(ctx context.Context, queries gen.Querier, labID uuid.UUID) (View, error) {
	lab, err := queries.GetLab(ctx, gen.GetLabParams{ID: labID, OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return View{}, shared.NotFoundOr(err, "lab")
	}
	byLab, err := scannersByLab(ctx, queries)
	if err != nil {
		return View{}, err
	}
	return viewOf(lab, byLab), nil
}

func validLabName(name string) (string, error) {
	trimmed := pointers.TrimmedOrNil(&name)
	if trimmed == nil {
		return "", apperror.Unprocessable("missing_fields", "name is required")
	}
	return *trimmed, nil
}

// saveScanners replaces the lab's scanners with the given ones, in order. Each must be a built-in
// scanner or one of the account's own; a scanner given twice keeps its first place. The default is
// optional and must be one of the given scanners.
func saveScanners(ctx context.Context, queries gen.Querier, labID uuid.UUID, scannerIDs *[]uuid.UUID, defaultID *uuid.UUID) error {
	if scannerIDs == nil {
		if defaultID != nil {
			return unknownDefaultScanner()
		}
		return nil
	}
	if defaultID != nil && !slices.Contains(*scannerIDs, *defaultID) {
		return unknownDefaultScanner()
	}
	keep := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, scannerID := range *scannerIDs {
		if seen[scannerID] {
			continue
		}
		seen[scannerID] = true
		_, err := queries.GetScanner(ctx, gen.GetScannerParams{ID: scannerID, OwnerID: requestctx.Owner(ctx)})
		if db.IsNoRows(err) {
			return apperror.Unprocessable("unknown_scanner", "scannerIds names a scanner that does not exist")
		}
		if err != nil {
			return err
		}
		keep = append(keep, scannerID)
	}
	if err := queries.DeleteLabScannersExcept(ctx, gen.DeleteLabScannersExceptParams{LabID: labID, Keep: keep}); err != nil {
		return err
	}
	if err := queries.ClearLabDefaultScanner(ctx, labID); err != nil {
		return err
	}
	for position, scannerID := range keep {
		isDefault := defaultID != nil && scannerID == *defaultID
		if err := queries.UpsertLabScanner(ctx, gen.UpsertLabScannerParams{LabID: labID, ScannerID: scannerID, Position: int32(position), IsDefault: isDefault}); err != nil {
			return err
		}
	}
	return nil
}

func unknownDefaultScanner() error {
	return apperror.Unprocessable("unknown_default_scanner", "defaultScannerId must be one of scannerIds")
}

// Create adds a lab (UC-23).
func (service *Service) Create(ctx context.Context, labID uuid.UUID, input Input) (View, error) {
	name, err := validLabName(input.Name)
	if err != nil {
		return View{}, err
	}
	var view View
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.InsertLab(ctx, gen.InsertLabParams{ID: labID, Name: name, Address: pointers.TrimmedOrNil(input.Address), OwnerID: requestctx.Owner(ctx)}); err != nil {
			return err
		}
		if err := saveScanners(ctx, queries, labID, input.ScannerIDs, input.DefaultScannerID); err != nil {
			return err
		}
		view, err = getView(ctx, queries, labID)
		return err
	})
	if db.IsUniqueViolation(err) {
		return View{}, apperror.Conflict("already_exists", "a lab with this id already exists")
	}
	return view, err
}

// Update edits a lab (UC-23).
func (service *Service) Update(ctx context.Context, labID uuid.UUID, input Input) (View, error) {
	name, err := validLabName(input.Name)
	if err != nil {
		return View{}, err
	}
	var view View
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.UpdateLab(ctx, gen.UpdateLabParams{ID: labID, Name: name, Address: pointers.TrimmedOrNil(input.Address), OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "lab")
		}
		if err := saveScanners(ctx, queries, labID, input.ScannerIDs, input.DefaultScannerID); err != nil {
			return err
		}
		view, err = getView(ctx, queries, labID)
		return err
	})
	return view, err
}

// Delete soft-deletes a lab that has no processing history (UC-23).
func (service *Service) Delete(ctx context.Context, labID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetLab(ctx, gen.GetLabParams{ID: labID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "lab")
		}
		used, err := queries.LabHasProcessing(ctx, gen.LabHasProcessingParams{LabID: labID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return err
		}
		if used {
			return apperror.Conflict("lab_in_use", "a lab with processing history cannot be deleted")
		}
		_, err = queries.SoftDeleteLab(ctx, gen.SoftDeleteLabParams{ID: labID, OwnerID: requestctx.Owner(ctx)})
		return err
	})
}
