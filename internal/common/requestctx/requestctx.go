// Package requestctx carries who made a request through the layers, so the database can
// stamp its audit trail without services having to know about it.
package requestctx

import "context"

// Meta identifies the caller and the request that causes a change.
type Meta struct {
	RequestID string
	Actor     string
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
