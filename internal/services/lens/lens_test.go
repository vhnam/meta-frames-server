package lens

import (
	"context"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
	"testing"

	"github.com/google/uuid"
)

func TestLensDelete(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	ctx := context.Background()
	free, used, builtIn, cameraID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	queries.Lenses[free] = gen.Lens{ID: free}
	queries.Lenses[used] = gen.Lens{ID: used}
	queries.Lenses[builtIn] = gen.Lens{ID: builtIn, IsBuiltIn: true}
	queries.Links = []gen.CameraLens{{CameraID: cameraID, LensID: free}}
	queries.RollLenses = []gen.RollLens{{RollID: uuid.New(), LensID: used}}

	if err := service.Delete(ctx, free); err != nil || !queries.Lenses[free].DeletedAt.Valid {
		test.Fatalf("err=%v lens=%+v", err, queries.Lenses[free])
	}
	if _, err := service.Get(ctx, free); err == nil {
		test.Fatal("a deleted lens must be invisible")
	}
	assertAppError(test, service.Delete(ctx, free), apperror.KindNotFound, "not_found")
	assertAppError(test, service.Delete(ctx, used), apperror.KindConflict, "lens_in_use")
	assertAppError(test, service.Delete(ctx, builtIn), apperror.KindConflict, "built_in_lens")
	assertAppError(test, service.Delete(ctx, uuid.New()), apperror.KindNotFound, "not_found")
}

func TestLensListAndGet(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	ctx := context.Background()
	activeID, inactiveID := uuid.New(), uuid.New()
	queries.Lenses[activeID] = gen.Lens{ID: activeID, IsActive: true}
	queries.Lenses[inactiveID] = gen.Lens{ID: inactiveID}

	if lenses, err := service.List(ctx, true, nil); err != nil || len(lenses) != 1 {
		test.Fatalf("active lenses=%v err=%v", lenses, err)
	}
	if lenses, err := service.List(ctx, false, pointers.To("F")); err != nil || len(lenses) != 2 {
		test.Fatalf("lenses=%v err=%v", lenses, err)
	}
	if lens, err := service.Get(ctx, activeID); err != nil || lens.ID != activeID {
		test.Fatalf("lens=%+v err=%v", lens, err)
	}
	assertAppError(test, func() error { _, err := service.Get(ctx, uuid.New()); return err }(), apperror.KindNotFound, "not_found")
}

// ---- scans ----

func TestLensCreateThenUpdate(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	lensID := uuid.New()
	input := Input{Brand: stringRef("Nikon"), Model: stringRef("50"), Mount: stringRef("F"), FocalLength: 50, MaxAperture: 1.4}

	lens, err := service.Create(context.Background(), lensID, input)
	if err != nil || lens.FocalLength != 50 || len(queries.Lenses) != 1 {
		test.Fatalf("create: lens=%+v err=%v", lens, err)
	}
	input.MaxAperture = 1.8
	lens, err = service.Update(context.Background(), lensID, input)
	if err != nil || lens.MaxAperture != 1.8 || len(queries.Lenses) != 1 {
		test.Fatalf("update: lens=%+v err=%v", lens, err)
	}
}

func TestLensUpdateRequiresAnExistingLens(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()})
	_, err := service.Update(context.Background(), uuid.New(), Input{Brand: stringRef("a"), Model: stringRef("b"), Mount: stringRef("c"), FocalLength: 50, MaxAperture: 1.4})
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestLensCreateAndUpdateRejectBadApertures(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()})
	input := Input{Brand: stringRef("a"), Model: stringRef("b"), Mount: stringRef("c"), FocalLength: 50, MaxAperture: 1.45}
	_, err := service.Create(context.Background(), uuid.New(), input)
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_aperture")
	_, err = service.Update(context.Background(), uuid.New(), input)
	assertAppError(test, err, apperror.KindUnprocessable, "invalid_aperture")
}

func TestLensCreateRequiresIdentityForInterchangeableLenses(test *testing.T) {
	service := New(testutil.Store{Querier: newMemoryQueries()})
	_, err := service.Create(context.Background(), uuid.New(), Input{FocalLength: 50, MaxAperture: 1.4})
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
}

func TestLensUpdateAllowsEditingABuiltInLensWithoutBrand(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	lensID := uuid.New()
	queries.Lenses[lensID] = gen.Lens{ID: lensID, IsBuiltIn: true, FocalLength: 40, MaxAperture: 1.7}

	lens, err := service.Update(context.Background(), lensID, Input{FocalLength: 38, MaxAperture: 1.8})
	if err != nil || lens.FocalLength != 38 {
		test.Fatalf("lens=%+v err=%v", lens, err)
	}
	_, err = service.Update(context.Background(), lensID, Input{Mount: stringRef("F"), FocalLength: 38, MaxAperture: 1.8})
	assertAppError(test, err, apperror.KindUnprocessable, "mount_not_allowed")
}

func TestLensSetActiveRefusesBuiltInLenses(test *testing.T) {
	queries := newMemoryQueries()
	service := New(testutil.Store{Querier: queries})
	builtInID, normalID := uuid.New(), uuid.New()
	queries.Lenses[builtInID] = gen.Lens{ID: builtInID, IsBuiltIn: true, IsActive: true}
	queries.Lenses[normalID] = gen.Lens{ID: normalID, IsActive: true}

	_, err := service.SetActive(context.Background(), builtInID, false)
	assertAppError(test, err, apperror.KindConflict, "built_in_lens")

	lens, err := service.SetActive(context.Background(), normalID, false)
	if err != nil || lens.IsActive {
		test.Fatalf("lens=%+v err=%v", lens, err)
	}
	_, err = service.SetActive(context.Background(), uuid.New(), true)
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestValidateAperture(test *testing.T) {
	cases := []struct {
		aperture float64
		wantErr  bool
	}{
		{1.4, false}, {1.7, false}, {2.8, false}, {2, false}, {16, false},
		{1.45, true}, {2.85, true}, {0.123, true},
	}
	for _, testCase := range cases {
		err := ValidateAperture(testCase.aperture)
		if (err != nil) != testCase.wantErr {
			test.Errorf("ValidateAperture(%v) error = %v, wantErr %v", testCase.aperture, err, testCase.wantErr)
		}
	}
}

func TestValidateLensIdentity(test *testing.T) {
	text := stringRef("x")
	if err := validateLensIdentity(false, text, text, text); err != nil {
		test.Fatalf("complete lens rejected: %v", err)
	}
	assertAppError(test, validateLensIdentity(false, nil, text, text), apperror.KindUnprocessable, "missing_fields")
	assertAppError(test, validateLensIdentity(false, text, text, nil), apperror.KindUnprocessable, "missing_fields")
	if err := validateLensIdentity(true, nil, nil, nil); err != nil {
		test.Fatalf("built-in lens without brand/model rejected: %v", err)
	}
	assertAppError(test, validateLensIdentity(true, nil, nil, text), apperror.KindUnprocessable, "mount_not_allowed")
}
