package db

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestTimestampTreatsNullAsTheZeroTime(test *testing.T) {
	if got := Timestamp(pgtype.Timestamptz{}); !got.IsZero() {
		test.Fatalf("got %v", got)
	}
	moment := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	if got := Timestamp(pgtype.Timestamptz{Time: moment, Valid: true}); !got.Equal(moment) {
		test.Fatalf("got %v", got)
	}
	// Valid decides, not Time: a NULL row can still carry a Time value.
	if got := Timestamp(pgtype.Timestamptz{Time: moment, Valid: false}); !got.IsZero() {
		test.Fatalf("an invalid timestamp must be the zero time, got %v", got)
	}
}
