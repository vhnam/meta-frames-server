package audit

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

type queries struct {
	gen.Querier
	got gen.ListAuditLogsParams
}

func (stub *queries) ListAuditLogs(_ context.Context, arg gen.ListAuditLogsParams) ([]gen.AuditLog, error) {
	stub.got = arg
	return []gen.AuditLog{{ID: 1, EntityType: "camera", Action: "create"}}, nil
}

func TestListAppliesDefaultsAndCaps(test *testing.T) {
	stub := &queries{}
	service := New(testutil.Store{Querier: stub})
	ctx := context.Background()

	if _, err := service.List(ctx, Filter{}); err != nil || stub.got.PageSize != DefaultLimit || stub.got.PageOffset != 0 {
		test.Fatalf("defaults: %+v err=%v", stub.got, err)
	}
	if _, err := service.List(ctx, Filter{Limit: 9999, Offset: -5}); err != nil || stub.got.PageSize != MaxLimit || stub.got.PageOffset != 0 {
		test.Fatalf("caps: %+v err=%v", stub.got, err)
	}
}

func TestListPassesFiltersThrough(test *testing.T) {
	stub := &queries{}
	service := New(testutil.Store{Querier: stub})
	entityType, action, id := "camera", "delete", uuid.New()

	rows, err := service.List(context.Background(), Filter{EntityType: &entityType, EntityID: &id, Action: &action, Limit: 10, Offset: 20})
	if err != nil || len(rows) != 1 {
		test.Fatalf("rows=%v err=%v", rows, err)
	}
	if *stub.got.EntityType != "camera" || *stub.got.EntityID != id || *stub.got.Action != "delete" || stub.got.PageSize != 10 || stub.got.PageOffset != 20 {
		test.Fatalf("params = %+v", stub.got)
	}
}

func (stub *queries) GetAuditLog(_ context.Context, id int64) (gen.AuditLog, error) {
	if id != 1 {
		return gen.AuditLog{}, pgx.ErrNoRows
	}
	return gen.AuditLog{ID: 1, EntityType: "camera"}, nil
}

func TestGetReturnsOneEntryOrNotFound(test *testing.T) {
	service := New(testutil.Store{Querier: &queries{}})
	if entry, err := service.Get(context.Background(), 1); err != nil || entry.EntityType != "camera" {
		test.Fatalf("entry=%+v err=%v", entry, err)
	}
	testutil.AssertAppError(test, func() error { _, err := service.Get(context.Background(), 2); return err }(), apperror.KindNotFound, "not_found")
}
