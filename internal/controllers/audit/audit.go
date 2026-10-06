package audit

import (
	"context"
	"encoding/json"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/convert"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	auditsvc "meta-frames-server/internal/services/audit"
)

// Controller serves the audit-log endpoint.
type Controller struct {
	audit *auditsvc.Service
}

// New builds the controller around its service.
func New(audit *auditsvc.Service) *Controller { return &Controller{audit: audit} }

func toAPIAuditLog(row gen.AuditLog) api.AuditLog {
	return api.AuditLog{
		Id: row.ID, EntityType: api.AuditEntityType(row.EntityType), EntityId: row.EntityID, Action: api.AuditAction(row.Action),
		Before: decodeSnapshot(row.Before), After: decodeSnapshot(row.After),
		Actor: row.Actor, RequestId: row.RequestID, CreatedAt: convert.Timestamp(row.CreatedAt),
	}
}

// decodeSnapshot turns a stored row snapshot back into a JSON object; nil stays absent.
func decodeSnapshot(raw []byte) *map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var snapshot map[string]interface{}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil
	}
	return &snapshot
}

func (controller *Controller) ListAuditLogs(ctx context.Context, request api.ListAuditLogsRequestObject) (api.ListAuditLogsResponseObject, error) {
	params := request.Params
	filter := auditsvc.Filter{EntityID: params.EntityId, Limit: pointers.Value(params.Limit), Offset: pointers.Value(params.Offset)}
	if params.EntityType != nil {
		filter.EntityType = pointers.To(string(*params.EntityType))
	}
	if params.Action != nil {
		filter.Action = pointers.To(string(*params.Action))
	}
	rows, err := controller.audit.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	mapped := make([]api.AuditLog, len(rows))
	for index, row := range rows {
		mapped[index] = toAPIAuditLog(row)
	}
	return api.ListAuditLogs200JSONResponse(mapped), nil
}

func (controller *Controller) GetAuditLog(ctx context.Context, request api.GetAuditLogRequestObject) (api.GetAuditLogResponseObject, error) {
	row, err := controller.audit.Get(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	return api.GetAuditLog200JSONResponse(toAPIAuditLog(row)), nil
}
