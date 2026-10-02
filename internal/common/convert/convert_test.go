package convert

import (
	"github.com/jackc/pgx/v5/pgtype"

	"meta-frames-server/internal/api"
	"testing"
	"time"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestOptionalDate(test *testing.T) {
	if OptionalDate(nil) != nil || OptionalDate(&api.DateInput{}) != nil {
		test.Fatal("a missing body or date means \"use the default\"")
	}
	given := APIDate(date(2026, time.May, 1))
	if got := OptionalDate(&api.DateInput{Date: &given}); got == nil || !got.Equal(date(2026, time.May, 1)) {
		test.Fatalf("got %v", got)
	}
}

func TestDateConversionsRoundTripAndKeepNil(test *testing.T) {
	if ToAPIDate(nil) != nil || FromAPIDate(nil) != nil {
		test.Fatal("nil must stay nil")
	}
	day := date(2026, time.March, 9)
	apiDate := ToAPIDate(&day)
	if apiDate == nil || !apiDate.Time.Equal(day) {
		test.Fatalf("ToAPIDate = %v", apiDate)
	}
	if back := FromAPIDate(apiDate); back == nil || !back.Equal(day) {
		test.Fatalf("FromAPIDate = %v", back)
	}
}

func TestTimestampTreatsNullAsTheZeroTime(test *testing.T) {
	if got := Timestamp(pgtype.Timestamptz{}); !got.IsZero() {
		test.Fatalf("got %v", got)
	}
	moment := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	if got := Timestamp(pgtype.Timestamptz{Time: moment, Valid: true}); !got.Equal(moment) {
		test.Fatalf("got %v", got)
	}
}
