package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"meta-frames-server/internal/common/requestctx"
	"meta-frames-server/internal/db/gen"
)

// Store is what services need from the database: plain queries and transactions.
type Store interface {
	Queries() gen.Querier
	// InTransaction runs work atomically; any returned error rolls everything back.
	InTransaction(ctx context.Context, work func(queries gen.Querier) error) error
}

type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *gen.Queries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, queries: gen.New(pool)}
}

func (store *PostgresStore) Queries() gen.Querier { return store.queries }

func (store *PostgresStore) InTransaction(ctx context.Context, work func(queries gen.Querier) error) error {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := stampAuditContext(ctx, transaction); err != nil {
		return err
	}
	if err := work(store.queries.WithTx(transaction)); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// stampAuditContext hands the caller's identity to the audit triggers for this transaction only
// (set_config(..., is_local => true) lasts until commit or rollback).
func stampAuditContext(ctx context.Context, transaction pgx.Tx) error {
	meta, ok := requestctx.From(ctx)
	if !ok {
		return nil
	}
	_, err := transaction.Exec(ctx, "SELECT set_config('app.request_id', $1, true), set_config('app.actor', $2, true)", meta.RequestID, meta.Actor)
	return err
}

func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func IsUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}
