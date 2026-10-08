package lab

import (
	"context"

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

func (service *Service) List(ctx context.Context) ([]gen.Lab, error) {
	return service.store.Queries().ListLabs(ctx, requestctx.Owner(ctx))
}

func (service *Service) Get(ctx context.Context, labID uuid.UUID) (gen.Lab, error) {
	lab, err := service.store.Queries().GetLab(ctx, gen.GetLabParams{ID: labID, OwnerID: requestctx.Owner(ctx)})
	return lab, shared.NotFoundOr(err, "lab")
}

func validLabName(name string) (string, error) {
	trimmed := pointers.TrimmedOrNil(&name)
	if trimmed == nil {
		return "", apperror.Unprocessable("missing_fields", "name is required")
	}
	return *trimmed, nil
}

// Create adds a lab (UC-23).
func (service *Service) Create(ctx context.Context, labID uuid.UUID, name string, address *string) (gen.Lab, error) {
	trimmed, err := validLabName(name)
	if err != nil {
		return gen.Lab{}, err
	}
	var lab gen.Lab
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		lab, err = queries.InsertLab(ctx, gen.InsertLabParams{ID: labID, Name: trimmed, Address: pointers.TrimmedOrNil(address), OwnerID: requestctx.Owner(ctx)})
		return err
	})
	if db.IsUniqueViolation(err) {
		return gen.Lab{}, apperror.Conflict("already_exists", "a lab with this id already exists")
	}
	return lab, err
}

// Update edits a lab (UC-23).
func (service *Service) Update(ctx context.Context, labID uuid.UUID, name string, address *string) (gen.Lab, error) {
	trimmed, err := validLabName(name)
	if err != nil {
		return gen.Lab{}, err
	}
	var lab gen.Lab
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		lab, err = queries.UpdateLab(ctx, gen.UpdateLabParams{ID: labID, Name: trimmed, Address: pointers.TrimmedOrNil(address), OwnerID: requestctx.Owner(ctx)})
		return err
	})
	return lab, shared.NotFoundOr(err, "lab")
}

// Delete soft-deletes a lab that has no processing history (UC-23).
func (service *Service) Delete(ctx context.Context, labID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetLab(ctx, gen.GetLabParams{ID: labID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "lab")
		}
		used, err := queries.LabHasProcessing(ctx, gen.LabHasProcessingParams{LabID: &labID, OwnerID: requestctx.Owner(ctx)})
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
