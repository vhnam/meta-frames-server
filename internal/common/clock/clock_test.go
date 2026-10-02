package clock

import (
	"testing"
	"time"
)

func TestSystemTodayIsMidnightUTC(test *testing.T) {
	today := System{}.Today()
	if today.Hour() != 0 || today.Minute() != 0 || today.Second() != 0 || today.Location() != time.UTC {
		test.Fatalf("Today = %v", today)
	}
	if time.Since(today) > 48*time.Hour || time.Until(today) > 24*time.Hour {
		test.Fatalf("Today = %v is not close to now", today)
	}
}

func TestFixedAlwaysReturnsItsDate(test *testing.T) {
	date := time.Date(2026, time.October, 2, 0, 0, 0, 0, time.UTC)
	if got := (Fixed{Date: date}).Today(); !got.Equal(date) {
		test.Fatalf("Today = %v", got)
	}
}
