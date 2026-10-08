// Package requestctx carries who made a request through the layers, so services can limit every
// query to the caller's records and the database can stamp its audit trail.
package requestctx

import (
	"context"

	"github.com/google/uuid"
)

// Meta identifies the caller and the request that causes a change.
type Meta struct {
	RequestID string
	Actor     string
	// OwnerID is the signed-in account; every record a service reads or writes belongs to it.
	OwnerID uuid.UUID
}

type key struct{}

// With returns a context carrying meta.
func With(ctx context.Context, meta Meta) context.Context {
	return context.WithValue(ctx, key{}, meta)
}

// From returns the meta stored by With, if any.
func From(ctx context.Context) (Meta, bool) {
	meta, ok := ctx.Value(key{}).(Meta)
	return meta, ok
}

// Owner returns the signed-in account. Without one it is uuid.Nil, which owns no records, so
// reads find nothing and writes fail: a missing caller never sees or touches anyone's data.
func Owner(ctx context.Context) uuid.UUID {
	meta, _ := From(ctx)
	return meta.OwnerID
}
