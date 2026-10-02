package camera

import (
	"context"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
	"testing"

	"github.com/google/uuid"
)

func newCameraServiceForTest(queries *memoryQueries) *Service {
	service := New(testutil.Store{Querier: queries})
	counter := 0
	service.newID = func() uuid.UUID {
		counter++
		return uuid.MustParse("00000000-0000-0000-0000-00000000000" + string(rune('0'+counter)))
	}
	return service
}

func TestCreateAddsAFixedLensCameraWithItsBuiltInLens(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	cameraID := uuid.New()

	view, err := service.Create(context.Background(), cameraID, Input{
		Brand: " Canon ", Model: "Canonet QL17", HasFixedLens: true,
		FixedLens: &FixedLensInput{FocalLength: 40, MaxAperture: 1.7},
	})
	if err != nil {
		test.Fatalf("Create: %v", err)
	}
	if view.Camera.Brand != "Canon" || !view.Camera.IsActive {
		test.Fatalf("view = %+v", view)
	}
	if len(queries.Links) != 1 || queries.Links[0].CameraID != cameraID {
		test.Fatalf("links = %+v", queries.Links)
	}
	builtIn := queries.Lenses[queries.Links[0].LensID]
	if !builtIn.IsBuiltIn || builtIn.FocalLength != 40 || builtIn.Mount != nil {
		test.Fatalf("built-in lens = %+v", builtIn)
	}
	if view.BuiltInLensID == nil || *view.BuiltInLensID != builtIn.ID {
		test.Fatalf("BuiltInLensID = %v", view.BuiltInLensID)
	}
}

func TestCreateRequiresTheBuiltInLensForNewFixedLensCameras(test *testing.T) {
	service := newCameraServiceForTest(newMemoryQueries())
	_, err := service.Create(context.Background(), uuid.New(), Input{Brand: "Canon", Model: "QL17", HasFixedLens: true})
	assertAppError(test, err, apperror.KindUnprocessable, "fixed_lens_required")
}

func TestUpdateRefusesToChangeTheFixedLensFlag(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	cameraID := uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, Brand: "Nikon", Model: "FM2", Mount: stringRef("F")}

	_, err := service.Update(context.Background(), cameraID, Input{Brand: "Nikon", Model: "FM2", HasFixedLens: true})
	assertAppError(test, err, apperror.KindConflict, "fixed_lens_immutable")
}

func TestUpdateEditsAnExistingCamera(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	cameraID := uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, Brand: "Nikon", Model: "FM2", Mount: stringRef("F")}

	view, err := service.Update(context.Background(), cameraID, Input{Brand: "Nikon", Model: "FM3A", Mount: stringRef("F")})
	if err != nil {
		test.Fatal(err)
	}
	if view.Camera.Model != "FM3A" {
		test.Fatalf("model = %q", view.Camera.Model)
	}
}

func TestSetActiveBlocksDeactivatingALoadedCamera(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	cameraID := uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, IsActive: true}
	queries.LoadedCameras[cameraID] = true

	_, err := service.SetActive(context.Background(), cameraID, false)
	assertAppError(test, err, apperror.KindConflict, "camera_loaded")
	if !queries.Cameras[cameraID].IsActive {
		test.Fatal("camera must stay active")
	}
}

func TestSetActiveDeactivatesTheBuiltInLensWithItsCamera(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	cameraID, lensID := uuid.New(), uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, IsActive: true, HasFixedLens: true}
	queries.Lenses[lensID] = gen.Lens{ID: lensID, IsBuiltIn: true, IsActive: true}
	queries.Links = []gen.CameraLens{{CameraID: cameraID, LensID: lensID}}

	if _, err := service.SetActive(context.Background(), cameraID, false); err != nil {
		test.Fatalf("SetActive: %v", err)
	}
	if queries.Lenses[lensID].IsActive {
		test.Fatal("the built-in lens must follow its camera")
	}
}

func TestSetActiveUnknownCamera(test *testing.T) {
	service := newCameraServiceForTest(newMemoryQueries())
	_, err := service.SetActive(context.Background(), uuid.New(), true)
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestSetLensesRejectsFixedLensCameras(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	cameraID := uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, HasFixedLens: true}

	_, err := service.SetLenses(context.Background(), cameraID, nil)
	assertAppError(test, err, apperror.KindConflict, "fixed_lens_camera")
}

func TestUpdateRequiresAnExistingCameraAndCreateRejectsInvalidInput(test *testing.T) {
	service := newCameraServiceForTest(newMemoryQueries())
	ctx := context.Background()

	_, err := service.Update(ctx, uuid.New(), Input{Brand: "Nikon", Model: "FM2", Mount: stringRef("F")})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
	_, err = service.Create(ctx, uuid.New(), Input{Brand: "Nikon", Model: "FM2"})
	assertAppError(test, err, apperror.KindUnprocessable, "mount_required")
	_, err = service.Update(ctx, uuid.New(), Input{Brand: " "})
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
}

func TestCameraDeleteSoftDeletesTheCameraAndItsBuiltInLens(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	ctx := context.Background()
	cameraID, builtIn, mounted := uuid.New(), uuid.New(), uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID, HasFixedLens: true}
	queries.Lenses[builtIn] = gen.Lens{ID: builtIn, IsBuiltIn: true}
	queries.Lenses[mounted] = gen.Lens{ID: mounted}
	queries.Links = []gen.CameraLens{{CameraID: cameraID, LensID: builtIn}, {CameraID: cameraID, LensID: mounted}}

	if err := service.Delete(ctx, cameraID); err != nil {
		test.Fatal(err)
	}
	if !queries.Cameras[cameraID].DeletedAt.Valid {
		test.Fatal("the camera row must be kept with deleted_at set")
	}
	if !queries.Lenses[builtIn].DeletedAt.Valid {
		test.Fatal("the built-in lens must be soft-deleted with its camera")
	}
	if queries.Lenses[mounted].DeletedAt.Valid {
		test.Fatal("a mountable lens must survive its camera")
	}
	if len(queries.Links) != 2 {
		test.Fatalf("links must be kept for history, got %v", queries.Links)
	}
	if _, err := service.Get(ctx, cameraID); err == nil {
		test.Fatal("a deleted camera must be invisible")
	}
	if all, _ := service.List(ctx, false); len(all) != 0 {
		test.Fatalf("a deleted camera must not be listed, got %v", all)
	}
}

func TestCameraDeleteRefusesCamerasThatHeldRollsAndUnknownIDs(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	cameraID, rollID := uuid.New(), uuid.New()
	queries.Cameras[cameraID] = gen.Camera{ID: cameraID}
	queries.Rolls[rollID] = gen.Roll{ID: rollID, CameraID: &cameraID}

	assertAppError(test, service.Delete(context.Background(), cameraID), apperror.KindConflict, "camera_in_use")
	assertAppError(test, service.Delete(context.Background(), uuid.New()), apperror.KindNotFound, "not_found")
}

func TestCameraListGetAndLenses(test *testing.T) {
	queries := newMemoryQueries()
	service := newCameraServiceForTest(queries)
	ctx := context.Background()
	activeID, inactiveID, lensID := uuid.New(), uuid.New(), uuid.New()
	queries.Cameras[activeID] = gen.Camera{ID: activeID, Brand: "A", IsActive: true, Mount: pointers.To("F")}
	queries.Cameras[inactiveID] = gen.Camera{ID: inactiveID, Brand: "B", Mount: pointers.To("F")}
	queries.Lenses[lensID] = gen.Lens{ID: lensID, IsActive: true}

	all, err := service.List(ctx, false)
	if err != nil || len(all) != 2 {
		test.Fatalf("all=%v err=%v", all, err)
	}
	active, err := service.List(ctx, true)
	if err != nil || len(active) != 1 || active[0].Camera.ID != activeID {
		test.Fatalf("active=%v err=%v", active, err)
	}
	if view, err := service.Get(ctx, activeID); err != nil || view.Camera.ID != activeID {
		test.Fatalf("view=%+v err=%v", view, err)
	}
	if _, err := service.Get(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown camera must fail")
	}

	linked, err := service.SetLenses(ctx, activeID, []uuid.UUID{lensID, lensID})
	if err != nil || len(linked) != 1 {
		test.Fatalf("linked=%v err=%v", linked, err)
	}
	if listed, err := service.ListLenses(ctx, activeID); err != nil || len(listed) != 1 {
		test.Fatalf("listed=%v err=%v", listed, err)
	}
	if _, err := service.ListLenses(ctx, uuid.New()); err == nil {
		test.Fatal("an unknown camera must fail")
	}
	if _, err := service.SetLenses(ctx, activeID, []uuid.UUID{uuid.New()}); err == nil {
		test.Fatal("unknown lenses must be rejected")
	}
	if cleared, err := service.SetLenses(ctx, activeID, nil); err != nil || len(cleared) != 0 {
		test.Fatalf("cleared=%v err=%v", cleared, err)
	}
}

func TestCheckLinkableLensesRules(test *testing.T) {
	inactive := gen.Lens{ID: uuid.New()}
	builtIn := gen.Lens{ID: uuid.New(), IsBuiltIn: true, IsActive: true}
	assertAppError(test, checkLinkableLenses([]gen.Lens{builtIn}, []uuid.UUID{builtIn.ID}, nil), apperror.KindUnprocessable, "built_in_lens")
	assertAppError(test, checkLinkableLenses([]gen.Lens{inactive}, []uuid.UUID{inactive.ID}, nil), apperror.KindUnprocessable, "inactive_lens")
	if err := checkLinkableLenses([]gen.Lens{inactive}, []uuid.UUID{inactive.ID}, map[uuid.UUID]bool{inactive.ID: true}); err != nil {
		test.Fatalf("an already linked inactive lens may stay: %v", err)
	}
}

func TestValidateCameraInput(test *testing.T) {
	mount := stringRef("F")
	cases := []struct {
		name     string
		input    Input
		wantCode string
	}{
		{"valid interchangeable", Input{Brand: "Nikon", Model: "FM2", Mount: mount}, ""},
		{"valid fixed", Input{Brand: "Canon", Model: "QL17", HasFixedLens: true}, ""},
		{"missing brand", Input{Brand: " ", Model: "FM2", Mount: mount}, "missing_fields"},
		{"missing model", Input{Brand: "Nikon", Mount: mount}, "missing_fields"},
		{"interchangeable needs mount", Input{Brand: "Nikon", Model: "FM2"}, "mount_required"},
		{"fixed rejects mount", Input{Brand: "Canon", Model: "QL17", Mount: mount, HasFixedLens: true}, "mount_not_allowed"},
		{"fixed lens aperture", Input{Brand: "Canon", Model: "QL17", HasFixedLens: true, FixedLens: &FixedLensInput{FocalLength: 40, MaxAperture: 1.75}}, "invalid_aperture"},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			valid, err := validateCameraInput(testCase.input)
			if testCase.wantCode == "" {
				if err != nil {
					test.Fatalf("unexpected error: %v", err)
				}
				if valid.brand == "" || valid.model == "" {
					test.Fatalf("valid = %+v", valid)
				}
				return
			}
			assertAppError(test, err, apperror.KindUnprocessable, testCase.wantCode)
		})
	}
}

func TestCheckLinkableLenses(test *testing.T) {
	activeID, inactiveID, builtInID := uuid.New(), uuid.New(), uuid.New()
	active := gen.Lens{ID: activeID, IsActive: true}
	inactive := gen.Lens{ID: inactiveID}
	builtIn := gen.Lens{ID: builtInID, IsActive: true, IsBuiltIn: true}

	if err := checkLinkableLenses([]gen.Lens{active}, []uuid.UUID{activeID}, nil); err != nil {
		test.Fatalf("active lens rejected: %v", err)
	}
	assertAppError(test, checkLinkableLenses(nil, []uuid.UUID{activeID}, nil), apperror.KindUnprocessable, "unknown_lens")
	assertAppError(test, checkLinkableLenses([]gen.Lens{builtIn}, []uuid.UUID{builtInID}, nil), apperror.KindUnprocessable, "built_in_lens")
	assertAppError(test, checkLinkableLenses([]gen.Lens{inactive}, []uuid.UUID{inactiveID}, nil), apperror.KindUnprocessable, "inactive_lens")
	if err := checkLinkableLenses([]gen.Lens{inactive}, []uuid.UUID{inactiveID}, map[uuid.UUID]bool{inactiveID: true}); err != nil {
		test.Fatalf("an already linked inactive lens must stay linkable: %v", err)
	}
}
