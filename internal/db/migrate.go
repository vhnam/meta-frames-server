package db

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"meta-frames-server/migrations"
)

// Migrate applies the embedded goose migrations.
func Migrate(connCfg *pgx.ConnConfig) error {
	db := stdlib.OpenDB(*connCfg)
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, ".")
}
