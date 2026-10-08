package lab

import (
	"context"
	"errors"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLabGet(test *testing.T) {
	service, queries := newLabService()
	id := uuid.New()
	queries.labs[id] = gen.Lab{ID: id, Name: "Lab A"}
	if lab, err := service.Get(context.Background(), id); err != nil || lab.Name != "Lab A" {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}
	assertAppError(test, func() error { _, err := service.Get(context.Background(), uuid.New()); return err }(), apperror.KindNotFound, "not_found")
}

type labQueries struct {
	gen.Querier
	labs    map[uuid.UUID]gen.Lab
	inUse   bool
	deleted []uuid.UUID
	failOn  string
}

func (queries *labQueries) ListLabs(_ context.Context, _ uuid.UUID) ([]gen.Lab, error) {
	if queries.failOn == "list" {
		return nil, errBoom
	}
	result := []gen.Lab{}
	for _, lab := range queries.labs {
		result = append(result, lab)
	}
	return result, nil
}

func (queries *labQueries) InsertLab(_ context.Context, arg gen.InsertLabParams) (gen.Lab, error) {
	if queries.failOn == "upsert" {
		return gen.Lab{}, errBoom
	}
	lab := gen.Lab{ID: arg.ID, Name: arg.Name, Address: arg.Address}
	queries.labs[arg.ID] = lab
	return lab, nil
}

func (queries *labQueries) UpdateLab(_ context.Context, arg gen.UpdateLabParams) (gen.Lab, error) {
	if _, found := queries.labs[arg.ID]; !found {
		return gen.Lab{}, pgx.ErrNoRows
	}
	lab := gen.Lab{ID: arg.ID, Name: arg.Name, Address: arg.Address}
	queries.labs[arg.ID] = lab
	return lab, nil
}

func (queries *labQueries) GetLab(_ context.Context, arg gen.GetLabParams) (gen.Lab, error) {
	id := arg.ID
	lab, found := queries.labs[id]
	if !found {
		return gen.Lab{}, pgx.ErrNoRows
	}
	return lab, nil
}

func (queries *labQueries) LabHasProcessing(_ context.Context, arg gen.LabHasProcessingParams) (bool, error) {
	if queries.failOn == "used" {
		return false, errBoom
	}
	return queries.inUse, nil
}

func (queries *labQueries) SoftDeleteLab(_ context.Context, arg gen.SoftDeleteLabParams) (int64, error) {
	id := arg.ID
	queries.deleted = append(queries.deleted, id)
	delete(queries.labs, id)
	return 1, nil
}

func newLabService() (*Service, *labQueries) {
	queries := &labQueries{labs: map[uuid.UUID]gen.Lab{}}
	return New(testutil.Store{Querier: queries}), queries
}

func TestLabCreateTrimsAndUpdateEditsInPlace(test *testing.T) {
	service, queries := newLabService()
	id := uuid.New()
	address := "  1 Main St "
	lab, err := service.Create(context.Background(), id, "  Lab A ", &address)
	if err != nil || lab.Name != "Lab A" || *lab.Address != "1 Main St" {
		test.Fatalf("lab=%+v err=%v", lab, err)
	}
	updated, err := service.Update(context.Background(), id, "Lab B", nil)
	if err != nil || updated.Name != "Lab B" || updated.Address != nil || len(queries.labs) != 1 {
		test.Fatalf("updated=%+v err=%v", updated, err)
	}
}

func TestLabUpdateRequiresAnExistingLab(test *testing.T) {
	service, _ := newLabService()
	_, err := service.Update(context.Background(), uuid.New(), "Lab", nil)
	assertAppError(test, err, apperror.KindNotFound, "not_found")
}

func TestLabCreateAndUpdateRejectBlankNames(test *testing.T) {
	service, _ := newLabService()
	_, err := service.Create(context.Background(), uuid.New(), "   ", nil)
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
	_, err = service.Update(context.Background(), uuid.New(), "   ", nil)
	assertAppError(test, err, apperror.KindUnprocessable, "missing_fields")
}

func TestLabCreateReportsDuplicateIDsAndStoreErrors(test *testing.T) {
	service, queries := newLabService()
	queries.failOn = "upsert"
	if _, err := service.Create(context.Background(), uuid.New(), "x", nil); !errors.Is(err, errBoom) {
		test.Fatalf("create err = %v", err)
	}
	queries.failOn = "list"
	if _, err := service.List(context.Background()); !errors.Is(err, errBoom) {
		test.Fatalf("list err = %v", err)
	}
}

func TestLabListReturnsLabs(test *testing.T) {
	service, queries := newLabService()
	queries.labs[uuid.New()] = gen.Lab{Name: "A"}
	labs, err := service.List(context.Background())
	if err != nil || len(labs) != 1 {
		test.Fatalf("labs=%v err=%v", labs, err)
	}
}

func TestLabDelete(test *testing.T) {
	service, queries := newLabService()
	id := uuid.New()
	queries.labs[id] = gen.Lab{ID: id}

	queries.inUse = true
	if appError, ok := apperror.From(service.Delete(context.Background(), id)); !ok || appError.Code != "lab_in_use" {
		test.Fatalf("a lab with history must be a conflict, got %v", appError)
	}
	queries.inUse = false
	queries.failOn = "used"
	if err := service.Delete(context.Background(), id); !errors.Is(err, errBoom) {
		test.Fatalf("err = %v", err)
	}
	queries.failOn = ""
	if err := service.Delete(context.Background(), id); err != nil || len(queries.deleted) != 1 {
		test.Fatalf("err=%v deleted=%v", err, queries.deleted)
	}
	if appError, ok := apperror.From(service.Delete(context.Background(), uuid.New())); !ok || appError.Kind != apperror.KindNotFound {
		test.Fatalf("missing lab must be not found, got %v", appError)
	}
}
