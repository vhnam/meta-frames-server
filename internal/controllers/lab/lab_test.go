package lab

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db/gen"
	labsvc "meta-frames-server/internal/services/lab"
	"meta-frames-server/internal/testutil"
)

type queries struct {
	gen.Querier
	labs map[uuid.UUID]gen.Lab
	used bool
	fail bool
}

var errBoom = errors.New("boom")

func (stub *queries) ListLabs(context.Context) ([]gen.Lab, error) {
	if stub.fail {
		return nil, errBoom
	}
	result := []gen.Lab{}
	for _, lab := range stub.labs {
		result = append(result, lab)
	}
	return result, nil
}

func (stub *queries) InsertLab(_ context.Context, arg gen.InsertLabParams) (gen.Lab, error) {
	lab := gen.Lab{ID: arg.ID, Name: arg.Name, Address: arg.Address}
	stub.labs[arg.ID] = lab
	return lab, nil
}

func (stub *queries) UpdateLab(_ context.Context, arg gen.UpdateLabParams) (gen.Lab, error) {
	if _, found := stub.labs[arg.ID]; !found {
		return gen.Lab{}, pgx.ErrNoRows
	}
	lab := gen.Lab{ID: arg.ID, Name: arg.Name, Address: arg.Address}
	stub.labs[arg.ID] = lab
	return lab, nil
}

func (stub *queries) GetLab(_ context.Context, id uuid.UUID) (gen.Lab, error) {
	if lab, found := stub.labs[id]; found {
		return lab, nil
	}
	return gen.Lab{}, pgx.ErrNoRows
}

func (stub *queries) LabHasProcessing(context.Context, *uuid.UUID) (bool, error) {
	return stub.used, nil
}

func (stub *queries) SoftDeleteLab(_ context.Context, id uuid.UUID) (int64, error) {
	delete(stub.labs, id)
	return 1, nil
}

func newController() (*Controller, *queries) {
	stub := &queries{labs: map[uuid.UUID]gen.Lab{}}
	return New(labsvc.New(testutil.Store{Querier: stub})), stub
}

func TestPutLabCreatesThenUpdates(test *testing.T) {
	controller, _ := newController()
	id := uuid.New()
	request := api.PutLabRequestObject{Id: id, Body: &api.LabInput{Name: "Lab A"}}

	first, err := controller.PutLab(context.Background(), request)
	if err != nil {
		test.Fatal(err)
	}
	if created, ok := first.(api.PutLab201JSONResponse); !ok || created.Name != "Lab A" {
		test.Fatalf("first = %#v", first)
	}
	second, _ := controller.PutLab(context.Background(), request)
	if _, ok := second.(api.PutLab200JSONResponse); !ok {
		test.Fatalf("second = %#v", second)
	}
}

func TestPutLabRejectsBlankName(test *testing.T) {
	controller, _ := newController()
	if _, err := controller.PutLab(context.Background(), api.PutLabRequestObject{Id: uuid.New(), Body: &api.LabInput{Name: " "}}); err == nil {
		test.Fatal("expected a validation error")
	}
}

func TestListLabs(test *testing.T) {
	controller, stub := newController()
	stub.labs[uuid.New()] = gen.Lab{Name: "Lab A"}
	response, err := controller.ListLabs(context.Background(), api.ListLabsRequestObject{})
	if err != nil || len(response.(api.ListLabs200JSONResponse)) != 1 {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	stub.fail = true
	if _, err := controller.ListLabs(context.Background(), api.ListLabsRequestObject{}); !errors.Is(err, errBoom) {
		test.Fatalf("err = %v", err)
	}
}

func TestDeleteLab(test *testing.T) {
	controller, stub := newController()
	id := uuid.New()
	stub.labs[id] = gen.Lab{ID: id}

	response, err := controller.DeleteLab(context.Background(), api.DeleteLabRequestObject{Id: id})
	if _, ok := response.(api.DeleteLab204Response); err != nil || !ok {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.DeleteLab(context.Background(), api.DeleteLabRequestObject{Id: id}); err == nil {
		test.Fatal("deleting a missing lab must fail")
	}
}

func TestGetLab(test *testing.T) {
	controller, stub := newController()
	id := uuid.New()
	stub.labs[id] = gen.Lab{ID: id, Name: "Lab A"}

	response, err := controller.GetLab(context.Background(), api.GetLabRequestObject{Id: id})
	if err != nil || response.(api.GetLab200JSONResponse).Name != "Lab A" {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.GetLab(context.Background(), api.GetLabRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown lab must fail")
	}
}

func TestCreateLabAssignsAFreshID(test *testing.T) {
	controller, stub := newController()
	response, err := controller.CreateLab(context.Background(), api.CreateLabRequestObject{Body: &api.LabInput{Name: "Lab A"}})
	if err != nil {
		test.Fatal(err)
	}
	if created := response.(api.CreateLab201JSONResponse); created.Id == uuid.Nil || len(stub.labs) != 1 {
		test.Fatalf("created = %+v", created)
	}
	if _, err := controller.CreateLab(context.Background(), api.CreateLabRequestObject{Body: &api.LabInput{Name: " "}}); err == nil {
		test.Fatal("expected a validation error")
	}
}
