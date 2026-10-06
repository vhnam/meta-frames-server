// Package testutil holds helpers shared by unit tests of the layers above the database.
package testutil

import (
	"context"

	"meta-frames-server/internal/db/gen"
)

// Store is a db.Store over an in-memory Querier; transactions simply run the work directly.
// Tests pass a struct that embeds gen.Querier and overrides only the queries they expect,
// so an unexpected query panics instead of silently returning zero values.
type Store struct{ Querier gen.Querier }

func (store Store) Queries() gen.Querier { return store.Querier }

func (store Store) InTransaction(_ context.Context, work func(queries gen.Querier) error) error {
	return work(store.Querier)
}
