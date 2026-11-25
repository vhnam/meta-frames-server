package api

import (
	"testing"
	"time"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestOptionalDate(test *testing.T) {
	if OptionalDate(nil) != nil || OptionalDate(&DateInput{}) != nil {
		test.Fatal("a missing body or date means \"use the default\"")
	}
	given := APIDate(date(2026, time.May, 1))
	if got := OptionalDate(&DateInput{Date: &given}); got == nil || !got.Equal(date(2026, time.May, 1)) {
		test.Fatalf("got %v", got)
	}
}

func TestDateConversionsRoundTripAndKeepNil(test *testing.T) {
	if ToAPIDate(nil) != nil || FromAPIDate(nil) != nil {
		test.Fatal("nil must stay nil")
	}
	day := date(2026, time.March, 9)
	apiDate := ToAPIDate(&day)
	if apiDate == nil || !apiDate.Equal(day) {
		test.Fatalf("ToAPIDate = %v", apiDate)
	}
	if back := FromAPIDate(apiDate); back == nil || !back.Equal(day) {
		test.Fatalf("FromAPIDate = %v", back)
	}
}

func TestDateConversionsPreserveTimeValue(test *testing.T) {
	moment := time.Date(2026, time.March, 9, 13, 45, 0, 0, time.FixedZone("UTC+7", 7*60*60))
	apiDate := APIDate(moment)
	if apiDate.Time != moment {
		test.Fatalf("APIDate changed the time value: %v", apiDate.Time)
	}
	if got := ToAPIDate(&moment); got == nil || got.Time != moment {
		test.Fatalf("ToAPIDate changed the time value: %v", got)
	}
	if got := FromAPIDate(&apiDate); got == nil || *got != moment {
		test.Fatalf("FromAPIDate changed the time value: %v", got)
	}
	if got := OptionalDate(&DateInput{Date: &apiDate}); got == nil || *got != moment {
		test.Fatalf("OptionalDate changed the time value: %v", got)
	}
}
