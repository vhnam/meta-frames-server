package lens

import (
	"context"
	"math"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
}

func New(store db.Store) *Service { return &Service{store: store} }

// ValidateAperture requires at most one decimal place (UC-04), e.g. 1.4 or 2.8.
func ValidateAperture(aperture float64) error {
	scaled := aperture * apertureDecimalScale
	if math.Abs(scaled-math.Round(scaled)) > 1e-9 {
		return apperror.Unprocessable("invalid_aperture", "max aperture must have at most one decimal place")
	}
	return nil
}

func (service *Service) List(ctx context.Context, activeOnly bool, preferMount *string) ([]gen.Lens, error) {
	return service.store.Queries().ListLenses(ctx, gen.ListLensesParams{
		ActiveOnly: activeOnly, PreferMount: pointers.TrimmedOrNil(preferMount),
	})
}

func (service *Service) Get(ctx context.Context, lensID uuid.UUID) (gen.Lens, error) {
	lens, err := service.store.Queries().GetLens(ctx, lensID)
	return lens, shared.NotFoundOr(err, "lens")
}

// Create adds a lens (UC-04).
func (service *Service) Create(ctx context.Context, lensID uuid.UUID, input Input) (gen.Lens, error) {
	if err := ValidateAperture(input.MaxAperture); err != nil {
		return gen.Lens{}, err
	}
	brand, model, mount := pointers.TrimmedOrNil(input.Brand), pointers.TrimmedOrNil(input.Model), pointers.TrimmedOrNil(input.Mount)
	if err := validateLensIdentity(false, brand, model, mount); err != nil {
		return gen.Lens{}, err
	}
	var lens gen.Lens
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		var err error
		lens, err = queries.InsertLens(ctx, gen.InsertLensParams{
			ID: lensID, Brand: brand, Model: model, Mount: mount, Description: pointers.TrimmedOrNil(input.Description),
			FocalLength: int32(input.FocalLength), MaxAperture: input.MaxAperture,
		})
		return err
	})
	if db.IsUniqueViolation(err) {
		return gen.Lens{}, apperror.Conflict("already_exists", "a lens with this id already exists")
	}
	return lens, err
}

// Update edits a lens (UC-05).
func (service *Service) Update(ctx context.Context, lensID uuid.UUID, input Input) (gen.Lens, error) {
	if err := ValidateAperture(input.MaxAperture); err != nil {
		return gen.Lens{}, err
	}
	brand, model, mount := pointers.TrimmedOrNil(input.Brand), pointers.TrimmedOrNil(input.Model), pointers.TrimmedOrNil(input.Mount)

	var saved gen.Lens
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		existing, err := queries.GetLensForUpdate(ctx, lensID)
		if err != nil {
			return shared.NotFoundOr(err, "lens")
		}
		if err := validateLensIdentity(existing.IsBuiltIn, brand, model, mount); err != nil {
			return err
		}
		saved, err = queries.UpdateLens(ctx, gen.UpdateLensParams{
			ID: lensID, Brand: brand, Model: model, Mount: mount, Description: pointers.TrimmedOrNil(input.Description),
			FocalLength: int32(input.FocalLength), MaxAperture: input.MaxAperture,
		})
		return err
	})
	return saved, err
}

// validateLensIdentity applies the brand/model/mount rules, which differ for built-in lenses.
func validateLensIdentity(isBuiltIn bool, brand, model, mount *string) error {
	if isBuiltIn {
		if mount != nil {
			return apperror.Unprocessable("mount_not_allowed", "a built-in lens has no mount")
		}
		return nil
	}
	if brand == nil || model == nil || mount == nil {
		return apperror.Unprocessable("missing_fields", "brand, model and mount are required")
	}
	return nil
}

// SetActive activates or deactivates a lens (UC-06).
func (service *Service) SetActive(ctx context.Context, lensID uuid.UUID, active bool) (gen.Lens, error) {
	var saved gen.Lens
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		lens, err := queries.GetLensForUpdate(ctx, lensID)
		if err != nil {
			return shared.NotFoundOr(err, "lens")
		}
		if lens.IsBuiltIn {
			return apperror.Conflict("built_in_lens", "a built-in lens follows its camera; deactivate the camera instead")
		}
		saved, err = queries.SetLensActive(ctx, gen.SetLensActiveParams{ID: lensID, IsActive: active})
		return err
	})
	return saved, err
}

// Delete soft-deletes a lens that was never used on a roll. A built-in lens goes with its camera.
func (service *Service) Delete(ctx context.Context, lensID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		lens, err := queries.GetLensForUpdate(ctx, lensID)
		if err != nil {
			return shared.NotFoundOr(err, "lens")
		}
		if lens.IsBuiltIn {
			return apperror.Conflict("built_in_lens", "a built-in lens is deleted with its camera")
		}
		used, err := queries.LensIsOnRolls(ctx, lensID)
		if err != nil {
			return err
		}
		if used {
			return apperror.Conflict("lens_in_use", "a lens used on rolls cannot be deleted; deactivate it instead")
		}
		_, err = queries.SoftDeleteLens(ctx, lensID)
		return err
	})
}
