package camera

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/lens"
	"meta-frames-server/internal/services/shared"
)

type Service struct {
	store db.Store
	newID func() uuid.UUID
}

func New(store db.Store) *Service {
	return &Service{store: store, newID: uuid.New}
}

// validatedCamera is a camera input after trimming and rule checks.
type validatedCamera struct {
	brand string
	model string
	mount *string
}

// validateCameraInput applies the UC-01 rules.
func validateCameraInput(input Input) (validatedCamera, error) {
	brand := pointers.TrimmedOrNil(&input.Brand)
	model := pointers.TrimmedOrNil(&input.Model)
	if brand == nil || model == nil {
		return validatedCamera{}, apperror.Unprocessable("missing_fields", "brand and model are required")
	}
	mount := pointers.TrimmedOrNil(input.Mount)
	if input.HasFixedLens && mount != nil {
		return validatedCamera{}, apperror.Unprocessable("mount_not_allowed", "a fixed-lens camera has no mount")
	}
	if !input.HasFixedLens && mount == nil {
		return validatedCamera{}, apperror.Unprocessable("mount_required", "mount is required for interchangeable-lens cameras")
	}
	if input.FixedLens != nil {
		if err := lens.ValidateAperture(input.FixedLens.MaxAperture); err != nil {
			return validatedCamera{}, err
		}
	}
	return validatedCamera{brand: *brand, model: *model, mount: mount}, nil
}

// buildCameraViews adds built-in lens ids and loaded rolls (UC-09) to cameras.
func buildCameraViews(ctx context.Context, queries gen.Querier, cameras []gen.Camera) ([]View, error) {
	links, err := queries.ListBuiltInLensLinks(ctx, requestctx.Owner(ctx))
	if err != nil {
		return nil, err
	}
	builtInLensByCamera := make(map[uuid.UUID]uuid.UUID, len(links))
	for _, link := range links {
		builtInLensByCamera[link.CameraID] = link.LensID
	}

	loadedRows, err := queries.ListLoadedRolls(ctx, requestctx.Owner(ctx))
	if err != nil {
		return nil, err
	}
	loadedByCamera := make(map[uuid.UUID]*LoadedRoll, len(loadedRows))
	for _, row := range loadedRows {
		shotISO := row.BoxIso
		if row.ShotIso != nil {
			shotISO = *row.ShotIso
		}
		loadedByCamera[*row.CameraID] = &LoadedRoll{
			RollID: row.RollID, StockID: row.StockID, StockBrand: row.StockBrand, StockName: row.StockName,
			ShotISO: shotISO, StartedAt: row.StartedAt, DaysLoaded: row.DaysLoaded,
		}
	}

	views := make([]View, len(cameras))
	for index, camera := range cameras {
		views[index] = View{Camera: camera, LoadedRoll: loadedByCamera[camera.ID]}
		if lensID, found := builtInLensByCamera[camera.ID]; found {
			views[index].BuiltInLensID = &lensID
		}
	}
	return views, nil
}

func buildCameraView(ctx context.Context, queries gen.Querier, camera gen.Camera) (View, error) {
	views, err := buildCameraViews(ctx, queries, []gen.Camera{camera})
	if err != nil {
		return View{}, err
	}
	return views[0], nil
}

func (service *Service) List(ctx context.Context, activeOnly bool) ([]View, error) {
	cameras, err := service.store.Queries().ListCameras(ctx, requestctx.Owner(ctx))
	if err != nil {
		return nil, err
	}
	if activeOnly {
		active := make([]gen.Camera, 0, len(cameras))
		for _, camera := range cameras {
			if camera.IsActive {
				active = append(active, camera)
			}
		}
		cameras = active
	}
	return buildCameraViews(ctx, service.store.Queries(), cameras)
}

func (service *Service) Get(ctx context.Context, cameraID uuid.UUID) (View, error) {
	queries := service.store.Queries()
	camera, err := queries.GetCamera(ctx, gen.GetCameraParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)})
	if err != nil {
		return View{}, shared.NotFoundOr(err, "camera")
	}
	return buildCameraView(ctx, queries, camera)
}

// Create adds a camera; a fixed-lens camera also gets its built-in lens (UC-01, UC-07).
func (service *Service) Create(ctx context.Context, cameraID uuid.UUID, input Input) (View, error) {
	valid, err := validateCameraInput(input)
	if err != nil {
		return View{}, err
	}
	if input.HasFixedLens && input.FixedLens == nil {
		return View{}, apperror.Unprocessable("fixed_lens_required", "fixedLens is required for a fixed-lens camera")
	}

	var saved gen.Camera
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		saved, err = queries.InsertCamera(ctx, gen.InsertCameraParams{
			ID: cameraID, Brand: valid.brand, Model: valid.model, Mount: valid.mount,
			Description: pointers.TrimmedOrNil(input.Description), HasFixedLens: input.HasFixedLens,
			OwnerID: requestctx.Owner(ctx),
		})
		if err != nil || !input.HasFixedLens {
			return err
		}
		return service.createBuiltInLens(ctx, queries, saved.ID, *input.FixedLens)
	})
	if db.IsUniqueViolation(err) {
		return View{}, apperror.Conflict("already_exists", "a camera with this id already exists")
	}
	if err != nil {
		return View{}, err
	}
	return buildCameraView(ctx, service.store.Queries(), saved)
}

// Update edits a camera's details (UC-02). The fixed-lens flag cannot change.
func (service *Service) Update(ctx context.Context, cameraID uuid.UUID, input Input) (View, error) {
	valid, err := validateCameraInput(input)
	if err != nil {
		return View{}, err
	}

	var saved gen.Camera
	err = service.store.InTransaction(ctx, func(queries gen.Querier) error {
		existing, err := queries.GetCameraForUpdate(ctx, gen.GetCameraForUpdateParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return shared.NotFoundOr(err, "camera")
		}
		if existing.HasFixedLens != input.HasFixedLens {
			return apperror.Conflict("fixed_lens_immutable", "the fixed-lens flag cannot be changed after creation")
		}
		saved, err = queries.UpdateCamera(ctx, gen.UpdateCameraParams{
			ID: cameraID, Brand: valid.brand, Model: valid.model, Mount: valid.mount,
			Description: pointers.TrimmedOrNil(input.Description),
			OwnerID:     requestctx.Owner(ctx),
		})
		return err
	})
	if err != nil {
		return View{}, err
	}
	return buildCameraView(ctx, service.store.Queries(), saved)
}

// createBuiltInLens creates the lens of a fixed-lens camera and links it (UC-07).
func (service *Service) createBuiltInLens(ctx context.Context, queries gen.Querier, cameraID uuid.UUID, lens FixedLensInput) error {
	lensID := service.newID()
	if _, err := queries.InsertLens(ctx, gen.InsertLensParams{
		ID: lensID, Brand: pointers.TrimmedOrNil(lens.Brand), Model: pointers.TrimmedOrNil(lens.Model),
		FocalLength: int32(lens.FocalLength), MaxAperture: lens.MaxAperture, IsBuiltIn: true,
		OwnerID: requestctx.Owner(ctx),
	}); err != nil {
		return err
	}
	return queries.InsertCameraLens(ctx, gen.InsertCameraLensParams{CameraID: cameraID, LensID: lensID})
}

// SetActive activates or deactivates a camera (UC-03).
func (service *Service) SetActive(ctx context.Context, cameraID uuid.UUID, active bool) (View, error) {
	var saved gen.Camera
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetCameraForUpdate(ctx, gen.GetCameraForUpdateParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "camera")
		}
		if !active {
			loaded, err := queries.CameraIsLoaded(ctx, gen.CameraIsLoadedParams{CameraID: &cameraID, OwnerID: requestctx.Owner(ctx)})
			if err != nil {
				return err
			}
			if loaded {
				return apperror.Conflict("camera_loaded", "camera holds a roll; finish the roll first")
			}
		}
		var err error
		saved, err = queries.SetCameraActive(ctx, gen.SetCameraActiveParams{ID: cameraID, IsActive: active, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return err
		}
		// A built-in lens follows its camera (UC-06).
		return queries.SetBuiltInLensActive(ctx, gen.SetBuiltInLensActiveParams{CameraID: cameraID, IsActive: active, OwnerID: requestctx.Owner(ctx)})
	})
	if err != nil {
		return View{}, err
	}
	return buildCameraView(ctx, service.store.Queries(), saved)
}

func (service *Service) ListLenses(ctx context.Context, cameraID uuid.UUID) ([]gen.Lens, error) {
	queries := service.store.Queries()
	if _, err := queries.GetCamera(ctx, gen.GetCameraParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)}); err != nil {
		return nil, shared.NotFoundOr(err, "camera")
	}
	return queries.ListCameraLenses(ctx, gen.ListCameraLensesParams{CameraID: cameraID, OwnerID: requestctx.Owner(ctx)})
}

// SetLenses replaces the lenses linked to an interchangeable-lens camera (UC-08).
func (service *Service) SetLenses(ctx context.Context, cameraID uuid.UUID, lensIDs []uuid.UUID) ([]gen.Lens, error) {
	lensIDs = shared.RemoveDuplicates(lensIDs)
	var linked []gen.Lens
	err := service.store.InTransaction(ctx, func(queries gen.Querier) error {
		camera, err := queries.GetCameraForUpdate(ctx, gen.GetCameraForUpdateParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return shared.NotFoundOr(err, "camera")
		}
		if camera.HasFixedLens {
			return apperror.Conflict("fixed_lens_camera", "a fixed-lens camera has exactly one built-in lens")
		}
		currentLenses, err := queries.ListCameraLenses(ctx, gen.ListCameraLensesParams{CameraID: cameraID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return err
		}
		requested, err := queries.GetLensesByIDs(ctx, gen.GetLensesByIDsParams{Ids: lensIDs, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return err
		}
		if err := checkLinkableLenses(requested, lensIDs, shared.LensIDSet(currentLenses)); err != nil {
			return err
		}

		if err := queries.DeleteCameraLensesExcept(ctx, gen.DeleteCameraLensesExceptParams{CameraID: cameraID, Column2: lensIDs}); err != nil {
			return err
		}
		for _, lensID := range lensIDs {
			if err := queries.InsertCameraLens(ctx, gen.InsertCameraLensParams{CameraID: cameraID, LensID: lensID}); err != nil {
				return err
			}
		}
		linked, err = queries.ListCameraLenses(ctx, gen.ListCameraLensesParams{CameraID: cameraID, OwnerID: requestctx.Owner(ctx)})
		return err
	})
	return linked, err
}

// checkLinkableLenses enforces the UC-08 rules on the requested lens set.
func checkLinkableLenses(found []gen.Lens, requestedIDs []uuid.UUID, alreadyLinked map[uuid.UUID]bool) error {
	if len(found) != len(requestedIDs) {
		return apperror.Unprocessable("unknown_lens", "one or more lenses do not exist")
	}
	for _, lens := range found {
		if lens.IsBuiltIn {
			return apperror.Unprocessable("built_in_lens", "a built-in lens cannot be linked to another camera")
		}
		if !lens.IsActive && !alreadyLinked[lens.ID] {
			return apperror.Unprocessable("inactive_lens", "an inactive lens cannot be newly linked")
		}
	}
	return nil
}

// Delete soft-deletes a camera that never held a roll, together with its built-in lens (UC-01 clean-up).
func (service *Service) Delete(ctx context.Context, cameraID uuid.UUID) error {
	return service.store.InTransaction(ctx, func(queries gen.Querier) error {
		if _, err := queries.GetCameraForUpdate(ctx, gen.GetCameraForUpdateParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return shared.NotFoundOr(err, "camera")
		}
		rolls, err := queries.CountCameraRolls(ctx, gen.CountCameraRollsParams{CameraID: &cameraID, OwnerID: requestctx.Owner(ctx)})
		if err != nil {
			return err
		}
		if rolls > 0 {
			return apperror.Conflict("camera_in_use", "a camera that has held rolls cannot be deleted; deactivate it instead")
		}
		if err := queries.SoftDeleteBuiltInLenses(ctx, gen.SoftDeleteBuiltInLensesParams{CameraID: cameraID, OwnerID: requestctx.Owner(ctx)}); err != nil {
			return err
		}
		_, err = queries.SoftDeleteCamera(ctx, gen.SoftDeleteCameraParams{ID: cameraID, OwnerID: requestctx.Owner(ctx)})
		return err
	})
}
