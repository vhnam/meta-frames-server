package testutil

import (
	"testing"
	"time"

	"meta-frames-server/internal/common/apperror"
)

// AssertAppError fails the test unless err is an application error of the given kind and code.
func AssertAppError(test *testing.T, err error, wantKind apperror.Kind, wantCode string) {
	test.Helper()
	appError, ok := apperror.From(err)
	if !ok {
		test.Fatalf("err = %v, want an app error with code %q", err, wantCode)
	}
	if appError.Kind != wantKind || appError.Code != wantCode {
		test.Fatalf("got kind=%v code=%q, want kind=%v code=%q", appError.Kind, appError.Code, wantKind, wantCode)
	}
}

// Day builds a calendar date at midnight UTC.
func Day(year int, month time.Month, dayOfMonth int) time.Time {
	return time.Date(year, month, dayOfMonth, 0, 0, 0, 0, time.UTC)
}
