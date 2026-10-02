package apperror_test

import (
	"errors"
	"fmt"
	"testing"

	"meta-frames-server/internal/common/apperror"
)

func TestConstructorsSetKindAndCode(test *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind apperror.Kind
		wantCode string
	}{
		{"not found", apperror.NotFound("camera"), apperror.KindNotFound, "not_found"},
		{"conflict", apperror.Conflict("camera_loaded", "busy"), apperror.KindConflict, "camera_loaded"},
		{"unprocessable", apperror.Unprocessable("bad", "nope"), apperror.KindUnprocessable, "bad"},
	}
	for _, tc := range cases {
		test.Run(tc.name, func(test *testing.T) {
			got, ok := apperror.From(tc.err)
			if !ok {
				test.Fatalf("From(%v) = not an app error", tc.err)
			}
			if got.Kind != tc.wantKind || got.Code != tc.wantCode {
				test.Fatalf("got kind=%v code=%q, want kind=%v code=%q", got.Kind, got.Code, tc.wantKind, tc.wantCode)
			}
		})
	}
}

func TestNotFoundMessageNamesTheResource(test *testing.T) {
	got, _ := apperror.From(apperror.NotFound("lens"))
	if got.Message != "lens not found" {
		test.Fatalf("message = %q", got.Message)
	}
}

func TestFromUnwrapsWrappedErrors(test *testing.T) {
	wrapped := fmt.Errorf("context: %w", apperror.Conflict("x", "y"))
	if got, ok := apperror.From(wrapped); !ok || got.Code != "x" {
		test.Fatalf("From(wrapped) = %v, %v", got, ok)
	}
}

func TestFromRejectsForeignErrors(test *testing.T) {
	if _, ok := apperror.From(errors.New("boom")); ok {
		test.Fatal("a plain error must not be treated as an app error")
	}
}
