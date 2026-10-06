package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

// ConfigurePool uses the simple query protocol so the pool is safe behind PgBouncer in
// transaction mode, and teaches that protocol how to encode uuid[]. An empty []uuid.UUID
// has no element pgx can inspect, so without this registration a filter that matches
// nothing (GET /inventory on an empty catalog) fails while encoding the argument.
func ConfigurePool(config *pgxpool.Config) {
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	config.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		registerUUIDArray(conn.TypeMap())
		return nil
	}
}

func registerUUIDArray(typeMap *pgtype.Map) {
	typeMap.RegisterDefaultPgType([]uuid.UUID{}, "_uuid")
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
