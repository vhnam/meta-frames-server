// Package requestctx carries who made a request through the layers, so services can limit every
// query to the caller's records and the database can stamp its audit trail.
package requestctx

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Meta identifies the caller and the request that causes a change.
type Meta struct {
	RequestID string
	Actor     string
	// OwnerID is the signed-in account; every record a service reads or writes belongs to it.
	OwnerID uuid.UUID
	// ExpectedCurrency holds the raw Expected-Currency header values, unparsed, joined by a newline
	// (a header value cannot hold one). ExpectedCurrencySent tells "no header" from an empty value.
	// Both are plain values so that Meta stays comparable. Read them with ExpectedCurrencyValues.
	ExpectedCurrency     string
	ExpectedCurrencySent bool
}

// WithExpectedCurrency returns meta carrying the Expected-Currency header values of a request.
func (meta Meta) WithExpectedCurrency(values []string) Meta {
	meta.ExpectedCurrency, meta.ExpectedCurrencySent = strings.Join(values, "\n"), len(values) > 0
	return meta
}

// ExpectedCurrencyValues returns the header values, one per header line, or nil without the header.
func (meta Meta) ExpectedCurrencyValues() []string {
	if !meta.ExpectedCurrencySent {
		return nil
	}
	return strings.Split(meta.ExpectedCurrency, "\n")
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
