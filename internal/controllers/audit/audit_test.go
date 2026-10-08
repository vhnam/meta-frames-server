package audit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	auditsvc "meta-frames-server/internal/services/audit"
	"meta-frames-server/internal/testutil"
)

type queries struct {
	gen.Querier
	rows []gen.AuditLog
	got  gen.ListAuditLogsParams
}

func (stub *queries) ListAuditLogs(_ context.Context, arg gen.ListAuditLogsParams) ([]gen.AuditLog, error) {
	stub.got = arg
	return stub.rows, nil
}

func TestListAuditLogsMapsRowsAndSnapshots(test *testing.T) {
	entityID := uuid.New()
	stub := &queries{rows: []gen.AuditLog{{
		ID: 7, EntityType: "camera", EntityID: entityID, Action: "update",
		Before: []byte(`{"model":"FM2"}`), After: []byte(`{"model":"FM3A"}`),
		Actor: pointers.To("nam"), RequestID: pointers.To("req-1"),
		CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), Valid: true},
	}, {
		ID: 6, EntityType: "camera", EntityID: entityID, Action: "create", After: []byte(`not json`),
	}}}
	controller := New(auditsvc.New(testutil.Store{Querier: stub}))

	response, err := controller.ListAuditLogs(context.Background(), api.ListAuditLogsRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	logs := response.(api.ListAuditLogs200JSONResponse)
	if len(logs) != 2 || logs[0].Action != api.AuditAction("update") || *logs[0].Actor != "nam" || *logs[0].RequestId != "req-1" {
		test.Fatalf("logs = %+v", logs)
	}
	if (*logs[0].Before)["model"] != "FM2" || (*logs[0].After)["model"] != "FM3A" {
		test.Fatalf("snapshots = %v / %v", logs[0].Before, logs[0].After)
	}
	if logs[1].Before != nil || logs[1].After != nil {
		test.Fatalf("a missing or malformed snapshot must be absent, got %v / %v", logs[1].Before, logs[1].After)
	}
}

func TestListAuditLogsForwardsTheFilters(test *testing.T) {
	stub := &queries{}
	controller := New(auditsvc.New(testutil.Store{Querier: stub}))
	id := uuid.New()
	entityType, action := api.AuditEntityType("lens"), api.AuditAction("delete")

	_, err := controller.ListAuditLogs(context.Background(), api.ListAuditLogsRequestObject{Params: api.ListAuditLogsParams{
		EntityType: &entityType, EntityId: &id, Action: &action, Limit: pointers.To(5), Offset: pointers.To(10),
	}})
	if err != nil {
		test.Fatal(err)
	}
	if *stub.got.EntityType != "lens" || *stub.got.EntityID != id || *stub.got.Action != "delete" || stub.got.PageSize != 5 || stub.got.PageOffset != 10 {
		test.Fatalf("params = %+v", stub.got)
	}
}

func (stub *queries) GetAuditLog(_ context.Context, arg gen.GetAuditLogParams) (gen.AuditLog, error) {
	id := arg.ID
	for _, row := range stub.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return gen.AuditLog{}, pgx.ErrNoRows
}

func TestGetAuditLog(test *testing.T) {
	stub := &queries{rows: []gen.AuditLog{{ID: 5, EntityType: "lab", Action: "create", After: []byte(`{"name":"Lab A"}`)}}}
	controller := New(auditsvc.New(testutil.Store{Querier: stub}))

	response, err := controller.GetAuditLog(context.Background(), api.GetAuditLogRequestObject{Id: 5})
	if err != nil || (*response.(api.GetAuditLog200JSONResponse).After)["name"] != "Lab A" {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.GetAuditLog(context.Background(), api.GetAuditLogRequestObject{Id: 99}); err == nil {
		test.Fatal("an unknown entry must fail")
	}
}
