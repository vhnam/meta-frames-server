package lens

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	lenssvc "meta-frames-server/internal/services/lens"
	"meta-frames-server/internal/testutil"
)

type queries struct {
	gen.Querier
	lenses map[uuid.UUID]gen.Lens
	listed gen.ListLensesParams
}

func (stub *queries) ListLenses(_ context.Context, arg gen.ListLensesParams) ([]gen.Lens, error) {
	stub.listed = arg
	result := []gen.Lens{}
	for _, lens := range stub.lenses {
		result = append(result, lens)
	}
	return result, nil
}

func (stub *queries) GetLens(_ context.Context, id uuid.UUID) (gen.Lens, error) {
	if lens, found := stub.lenses[id]; found {
		return lens, nil
	}
	return gen.Lens{}, pgx.ErrNoRows
}

func (stub *queries) GetLensForUpdate(ctx context.Context, id uuid.UUID) (gen.Lens, error) {
	return stub.GetLens(ctx, id)
}

func (stub *queries) InsertLens(_ context.Context, arg gen.InsertLensParams) (gen.Lens, error) {
	lens := gen.Lens{ID: arg.ID, Brand: arg.Brand, Model: arg.Model, Mount: arg.Mount, FocalLength: arg.FocalLength, MaxAperture: arg.MaxAperture, IsActive: true}
	stub.lenses[arg.ID] = lens
	return lens, nil
}

func (stub *queries) UpdateLens(_ context.Context, arg gen.UpdateLensParams) (gen.Lens, error) {
	lens := stub.lenses[arg.ID]
	lens.Brand, lens.Model, lens.Mount, lens.FocalLength, lens.MaxAperture = arg.Brand, arg.Model, arg.Mount, arg.FocalLength, arg.MaxAperture
	stub.lenses[arg.ID] = lens
	return lens, nil
}

func (stub *queries) SetLensActive(_ context.Context, arg gen.SetLensActiveParams) (gen.Lens, error) {
	lens := stub.lenses[arg.ID]
	lens.IsActive = arg.IsActive
	stub.lenses[arg.ID] = lens
	return lens, nil
}

func newController() (*Controller, *queries) {
	stub := &queries{lenses: map[uuid.UUID]gen.Lens{}}
	return New(lenssvc.New(testutil.Store{Querier: stub})), stub
}

func lensInput() *api.LensInput {
	return &api.LensInput{Brand: pointers.To("Nikon"), Model: pointers.To("50mm"), Mount: pointers.To("F"), FocalLength: 50, MaxAperture: 1.8}
}

func TestPutLensCreatesThenUpdates(test *testing.T) {
	controller, _ := newController()
	id := uuid.New()
	request := api.PutLensRequestObject{Id: id, Body: lensInput()}

	first, err := controller.PutLens(context.Background(), request)
	if created, ok := first.(api.PutLens201JSONResponse); err != nil || !ok || created.FocalLength != 50 {
		test.Fatalf("first=%#v err=%v", first, err)
	}
	second, _ := controller.PutLens(context.Background(), request)
	if _, ok := second.(api.PutLens200JSONResponse); !ok {
		test.Fatalf("second = %#v", second)
	}
}

func TestPutLensRejectsAnApertureWithTwoDecimals(test *testing.T) {
	controller, _ := newController()
	body := lensInput()
	body.MaxAperture = 1.85
	if _, err := controller.PutLens(context.Background(), api.PutLensRequestObject{Id: uuid.New(), Body: body}); err == nil {
		test.Fatal("expected invalid_aperture")
	}
}

func TestGetAndListLenses(test *testing.T) {
	controller, stub := newController()
	id := uuid.New()
	stub.lenses[id] = gen.Lens{ID: id, FocalLength: 35}

	got, err := controller.GetLens(context.Background(), api.GetLensRequestObject{Id: id})
	if err != nil || got.(api.GetLens200JSONResponse).FocalLength != 35 {
		test.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := controller.GetLens(context.Background(), api.GetLensRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("a missing lens must be an error")
	}

	activeOnly, mount := true, " F "
	listed, err := controller.ListLenses(context.Background(), api.ListLensesRequestObject{Params: api.ListLensesParams{ActiveOnly: &activeOnly, PreferMount: &mount}})
	if err != nil || len(listed.(api.ListLenses200JSONResponse)) != 1 {
		test.Fatalf("listed=%#v err=%v", listed, err)
	}
	if !stub.listed.ActiveOnly || stub.listed.PreferMount == nil || *stub.listed.PreferMount != "F" {
		test.Fatalf("query params = %+v", stub.listed)
	}
	if _, err := controller.ListLenses(context.Background(), api.ListLensesRequestObject{}); err != nil || stub.listed.ActiveOnly {
		test.Fatalf("defaults must list every lens, got %+v err=%v", stub.listed, err)
	}
}

func TestSetLensActive(test *testing.T) {
	controller, stub := newController()
	id, builtIn := uuid.New(), uuid.New()
	stub.lenses[id] = gen.Lens{ID: id, IsActive: true}
	stub.lenses[builtIn] = gen.Lens{ID: builtIn, IsBuiltIn: true}

	response, err := controller.SetLensActive(context.Background(), api.SetLensActiveRequestObject{Id: id, Body: &api.ActiveInput{IsActive: false}})
	if err != nil || response.(api.SetLensActive200JSONResponse).IsActive {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.SetLensActive(context.Background(), api.SetLensActiveRequestObject{Id: builtIn, Body: &api.ActiveInput{IsActive: false}}); err == nil {
		test.Fatal("a built-in lens must not be toggled directly")
	}
	if _, err := controller.SetLensActive(context.Background(), api.SetLensActiveRequestObject{Id: uuid.New(), Body: &api.ActiveInput{}}); err == nil {
		test.Fatal("a missing lens must be an error")
	}
}

func (stub *queries) LensIsOnRolls(context.Context, uuid.UUID) (bool, error) { return false, nil }

func (stub *queries) SoftDeleteLens(_ context.Context, id uuid.UUID) (int64, error) {
	delete(stub.lenses, id)
	return 1, nil
}

func TestDeleteLens(test *testing.T) {
	controller, stub := newController()
	id, builtIn := uuid.New(), uuid.New()
	stub.lenses[id] = gen.Lens{ID: id}
	stub.lenses[builtIn] = gen.Lens{ID: builtIn, IsBuiltIn: true}

	response, err := controller.DeleteLens(context.Background(), api.DeleteLensRequestObject{Id: id})
	if _, ok := response.(api.DeleteLens204Response); err != nil || !ok {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.DeleteLens(context.Background(), api.DeleteLensRequestObject{Id: builtIn}); err == nil {
		test.Fatal("a built-in lens must not be deleted directly")
	}
	if _, err := controller.DeleteLens(context.Background(), api.DeleteLensRequestObject{Id: id}); err == nil {
		test.Fatal("deleting a missing lens must fail")
	}
}

func TestCreateLensAssignsAFreshID(test *testing.T) {
	controller, stub := newController()
	response, err := controller.CreateLens(context.Background(), api.CreateLensRequestObject{Body: lensInput()})
	if err != nil {
		test.Fatal(err)
	}
	if created := response.(api.CreateLens201JSONResponse); created.Id == uuid.Nil || len(stub.lenses) != 1 {
		test.Fatalf("created = %+v", created)
	}
	bad := lensInput()
	bad.MaxAperture = 1.85
	if _, err := controller.CreateLens(context.Background(), api.CreateLensRequestObject{Body: bad}); err == nil {
		test.Fatal("expected invalid_aperture")
	}
}
