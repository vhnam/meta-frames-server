// Package pointers has small helpers for optional values.
package pointers

import "strings"

// To returns a pointer to value.
func To[Value any](value Value) *Value { return &value }

// TrimmedOrNil trims text and returns nil when nothing is left.
func TrimmedOrNil(text *string) *string {
	if text == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*text)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// IsBlank reports whether text is empty after trimming.
func IsBlank(text string) bool { return strings.TrimSpace(text) == "" }

// Int32 converts an optional int to an optional int32.
func Int32(value *int) *int32 {
	if value == nil {
		return nil
	}
	return To(int32(*value))
}

// Int converts an optional int32 to an optional int.
func Int(value *int32) *int {
	if value == nil {
		return nil
	}
	return To(int(*value))
}

// Value dereferences value, returning the zero value for nil.
func Value[Value any](value *Value) Value {
	if value == nil {
		var zero Value
		return zero
	}
	return *value
}
