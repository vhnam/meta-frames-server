package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsNoRows(test *testing.T) {
	if !IsNoRows(pgx.ErrNoRows) || !IsNoRows(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)) {
		test.Fatal("pgx.ErrNoRows must be recognised, wrapped or not")
	}
	if IsNoRows(errors.New("other")) || IsNoRows(nil) {
		test.Fatal("other errors are not no-rows")
	}
}

func TestIsUniqueViolation(test *testing.T) {
	unique := &pgconn.PgError{Code: "23505"}
	if !IsUniqueViolation(unique) || !IsUniqueViolation(fmt.Errorf("wrapped: %w", unique)) {
		test.Fatal("SQLSTATE 23505 must be recognised")
	}
	if IsUniqueViolation(&pgconn.PgError{Code: "23503"}) || IsUniqueViolation(errors.New("other")) {
		test.Fatal("other errors are not unique violations")
	}
}
