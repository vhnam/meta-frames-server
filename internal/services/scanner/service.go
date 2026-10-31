// Package scanner manages scanners: the built-in catalog every account sees, plus each
// account's own (another lab's machine, a flatbed, a camera rig).
package scanner

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

// The account's stand-in for the old "other" code; migration 00011 creates it the same way.
const unknownBrand = "Unknown"

var unknownModel = "scanner"

type Service struct {
	store db.Store
	newID func() uuid.UUID
}

func New(store db.Store) *Service { return &Service{store: store, newID: uuid.New} }

// IsBuiltIn reports whether the scanner is part of the shared catalog (it has no owner).
func IsBuiltIn(scanner gen.Scanner) bool { return scanner.OwnerID == uuid.Nil }

func (service *Service) List(ctx context.Context) ([]gen.Scanner, error) {
	return service.store.Queries().ListScanners(ctx, requestctx.Owner(ctx))
}

func (service *Service) Get(ctx context.Context, scannerID uuid.UUID) (gen.Scanner, error) {
	scanner, err := service.store.Queries().GetScanner(ctx, gen.GetScannerParams{ID: scannerID, OwnerID: requestctx.Owner(ctx)})
	return scanner, shared.NotFoundOr(err, "scanner")
}

// validInput trims the input and checks the required fields and the kind.
func validInput(input Input) (Input, error) {
	brand := pointers.TrimmedOrNil(&input.Brand)
	if brand == nil {
		return Input{}, apperror.Unprocessable("missing_fields", "brand is required")
	}
	if input.Kind != nil && domain.ScanOptionsFor(input.Kind) == nil {
		return Input{}, apperror.Unprocessable("invalid_scanner_kind", "kind is not a known scanner kind")
	}
	if err := CheckSettings(input.Kind, input.DefaultSettings); err != nil {
		return Input{}, err
	}
	return Input{
		Brand: *brand, Model: pointers.TrimmedOrNil(input.Model), Kind: input.Kind, Notes: pointers.TrimmedOrNil(input.Notes), DefaultSettings: input.DefaultSettings,
	}, nil
}

// CheckSettings is unprocessable when settings do not fit the options of a scanner kind.
func CheckSettings(kind *string, settings domain.ScanSettings) error {
	if err := domain.CheckScanSettings(kind, settings); err != nil {
		return apperror.Unprocessable("invalid_scan_settings", err.Error())
	}
	return nil
}

// checkNameFree refuses the name of a built-in scanner; the unique index covers the account's own.
func checkNameFree(ctx context.Context, queries gen.Querier, input Input) error {
	taken, err := queries.BuiltInScannerNamed(ctx, gen.BuiltInScannerNamedParams{Brand: input.Brand, Model: input.Model})
	if err != nil {
		return err
	}
	if taken {
		return apperror.Conflict("already_exists", "a built-in scanner has this name")
	}
	return nil
}

func nameTaken(err error) error {
	if db.IsUniqueViolation(err) {
		return apperror.Conflict("already_exists", "a scanner with this id or name already exists")
	}
	return err
}

// Create adds a scanner of the account's own.
func (service *Service) Create(ctx context.Context, scannerID uuid.UUID, input Input) (gen.Scanner, error) {
	input, err := validInput(input)
	if err != nil {
		return gen.Scanner{}, err
	}
	var scanner gen.Scanner
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if err := checkNameFree(ctx, queries, input); err != nil {
			return err
		}
		scanner, err = queries.InsertScanner(ctx, gen.InsertScannerParams{
			ID: scannerID, Brand: input.Brand, Model: input.Model, Kind: input.Kind, Notes: input.Notes,
			DefaultSettings: domain.EncodeScanSettings(input.DefaultSettings), OwnerID: requestctx.Owner(ctx),
		})
		return err
	})
	return scanner, nameTaken(err)
}

// Update edits one of the account's scanners. Built-in scanners are read-only.
func (service *Service) Update(ctx context.Context, scannerID uuid.UUID, input Input) (gen.Scanner, error) {
	input, err := validInput(input)
	if err != nil {
		return gen.Scanner{}, err
	}
	var scanner gen.Scanner
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if err := refuseBuiltIn(ctx, queries, scannerID); err != nil {
			return err
		}
		if err := checkNameFree(ctx, queries, input); err != nil {
			return err
		}
		scanner, err = queries.UpdateScanner(ctx, gen.UpdateScannerParams{
			ID: scannerID, Brand: input.Brand, Model: input.Model, Kind: input.Kind, Notes: input.Notes,
			DefaultSettings: domain.EncodeScanSettings(input.DefaultSettings), OwnerID: requestctx.Owner(ctx),
		})
		return shared.NotFoundOr(err, "scanner")
	})
	return scanner, nameTaken(err)
}

// refuseBuiltIn is a conflict for a built-in scanner and nil for anything else, including an unknown id.
func refuseBuiltIn(ctx context.Context, queries gen.Querier, scannerID uuid.UUID) error {
	scanner, err := queries.GetScanner(ctx, gen.GetScannerParams{ID: scannerID, OwnerID: requestctx.Owner(ctx)})
	switch {
	case db.IsNoRows(err):
		return nil
	case err != nil:
		return err
	case IsBuiltIn(scanner):
		return apperror.Conflict("built_in_scanner", "built-in scanners cannot be changed")
	}
	return nil
}

// Delete soft-deletes one of the account's scanners that no job or scan uses. Labs that ran it
// lose it from their scanners.
func (service *Service) Delete(ctx context.Context, scannerID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if err := refuseBuiltIn(ctx, queries, scannerID); err != nil {
			return err
		}
		if _, err := queries.GetScanner(ctx, gen.GetScannerParams{ID: scannerID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "scanner")
		}
		used, err := queries.ScannerInUse(ctx, scannerID)
		if err != nil {
			return err
		}
		if used {
			return apperror.Conflict("scanner_in_use", "a scanner used by a job or a scan cannot be deleted")
		}
		if err := queries.RemoveScannerFromLabs(ctx, scannerID); err != nil {
			return err
		}
		_, err = queries.SoftDeleteScanner(ctx, gen.SoftDeleteScannerParams{ID: scannerID, OwnerID: requestctx.Owner(ctx)})
		return err
	})
}

// Pick returns the scanner a request names, by id or by old code; exactly one must be given.
// The id is returned as given: callers check that the account can use it. The code "other"
// stands for the account's "Unknown scanner" and is unknown_scanner when there is none.
func (service *Service) Pick(ctx context.Context, scannerID *uuid.UUID, code *string) (uuid.UUID, error) {
	return service.pick(ctx, scannerID, code, false)
}

// PickForOrder is Pick for ordering a scan, where "other" creates the "Unknown scanner" on first use.
func (service *Service) PickForOrder(ctx context.Context, scannerID *uuid.UUID, code *string) (uuid.UUID, error) {
	return service.pick(ctx, scannerID, code, true)
}

func (service *Service) pick(ctx context.Context, scannerID *uuid.UUID, code *string, create bool) (uuid.UUID, error) {
	if (scannerID == nil) == (code == nil) {
		return uuid.Nil, apperror.Unprocessable("invalid_scanner", "give either scannerId or scanner")
	}
	if scannerID != nil {
		return *scannerID, nil
	}
	switch *code {
	case domain.ScannerCodeNoritsu:
		return domain.ScannerNoritsuHS1800, nil
	case domain.ScannerCodeFrontier:
		return domain.ScannerFrontierSP3000, nil
	case domain.ScannerCodeOther:
		return service.unknownScanner(ctx, create)
	default:
		return uuid.Nil, apperror.Unprocessable("invalid_scanner", "scanner must be noritsu, frontier or other")
	}
}

func (service *Service) unknownScanner(ctx context.Context, create bool) (uuid.UUID, error) {
	named := gen.GetOwnScannerNamedParams{OwnerID: requestctx.Owner(ctx), Brand: unknownBrand, Model: &unknownModel}
	scanner, err := service.store.Queries().GetOwnScannerNamed(ctx, named)
	if err == nil {
		return scanner.ID, nil
	}
	if !db.IsNoRows(err) {
		return uuid.Nil, err
	}
	if !create {
		return uuid.Nil, apperror.Unprocessable("unknown_scanner", "scanner does not exist")
	}
	scanner, err = service.Create(ctx, service.newID(), Input{Brand: unknownBrand, Model: &unknownModel})
	if appError, ok := apperror.From(err); ok && appError.Code == "already_exists" {
		// created by a concurrent request in the meantime
		scanner, err = service.store.Queries().GetOwnScannerNamed(ctx, named)
	}
	return scanner.ID, err
}
