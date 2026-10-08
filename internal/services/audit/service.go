// Package audit reads the audit trail that database triggers write on every change.
package audit

import (
	"context"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services/shared"
)

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

type Service struct {
	store db.Store
}

func New(store db.Store) *Service { return &Service{store: store} }

// Filter narrows the audit trail; nil fields match everything.
type Filter struct {
	EntityType *string
	EntityID   *uuid.UUID
	Action     *string
	Limit      int
	Offset     int
}

// List returns audit entries, newest first.
func (service *Service) List(ctx context.Context, filter Filter) ([]gen.AuditLog, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset := max(filter.Offset, 0)
	return service.store.Queries().ListAuditLogs(ctx, gen.ListAuditLogsParams{
		EntityType: filter.EntityType, EntityID: filter.EntityID, Action: filter.Action,
		PageSize: int32(limit), PageOffset: int32(offset),
		OwnerID: requestctx.Owner(ctx),
	})
}

// Get returns one audit entry.
func (service *Service) Get(ctx context.Context, id int64) (gen.AuditLog, error) {
	entry, err := service.store.Queries().GetAuditLog(ctx, gen.GetAuditLogParams{ID: id, OwnerID: requestctx.Owner(ctx)})
	return entry, shared.NotFoundOr(err, "audit entry")
}
