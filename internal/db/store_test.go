package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestIsNoRows(test *testing.T) {
	if !IsNoRows(pgx.ErrNoRows) || !IsNoRows(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)) {
		test.Fatal("pgx.ErrNoRows must be recognised, wrapped or not")
	}
	if IsNoRows(errors.New("other")) || IsNoRows(nil) {
		test.Fatal("other errors are not no-rows")
	}
}

func TestUUIDArrayEncodesEmptySliceAsText(test *testing.T) {
	typeMap := pgtype.NewMap()
	registerUUIDArray(typeMap)
	id := uuid.MustParse("e1b7a4d9-0c58-4a32-b6f1-5d9c2e8a7b05")

	empty, err := typeMap.Encode(0, pgtype.TextFormatCode, []uuid.UUID{}, nil)
	if err != nil || string(empty) != "{}" {
		test.Fatalf("empty = %q err=%v", empty, err)
	}
	one, err := typeMap.Encode(0, pgtype.TextFormatCode, []uuid.UUID{id}, nil)
	if err != nil || string(one) != "{"+id.String()+"}" {
		test.Fatalf("one = %q err=%v", one, err)
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
