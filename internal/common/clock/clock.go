// Package clock lets business rules depend on "today" without reading the system clock directly.
package clock

import "time"

type Clock interface {
	// Today returns the current calendar date at midnight UTC.
	Today() time.Time
}

type System struct{}

func (System) Today() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// Fixed always reports the same date; it is meant for tests.
type Fixed struct{ Date time.Time }

func (fixed Fixed) Today() time.Time { return fixed.Date }
