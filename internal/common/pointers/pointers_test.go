package pointers_test

import (
	"testing"

	"meta-frames-server/internal/common/pointers"
)

func TestTrimmedOrNil(test *testing.T) {
	text := func(value string) *string { return &value }
	cases := []struct {
		name  string
		input *string
		want  *string
	}{
		{"nil stays nil", nil, nil},
		{"blank becomes nil", text("   "), nil},
		{"empty becomes nil", text(""), nil},
		{"text is trimmed", text("  Canon "), text("Canon")},
	}
	for _, tc := range cases {
		test.Run(tc.name, func(test *testing.T) {
			got := pointers.TrimmedOrNil(tc.input)
			switch {
			case got == nil && tc.want == nil:
			case got == nil || tc.want == nil || *got != *tc.want:
				test.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsBlank(test *testing.T) {
	if !pointers.IsBlank(" \t") || pointers.IsBlank(" a ") {
		test.Fatal("IsBlank misclassified input")
	}
}

func TestIntConversions(test *testing.T) {
	if pointers.Int32(nil) != nil || pointers.Int(nil) != nil {
		test.Fatal("nil must stay nil")
	}
	if got := *pointers.Int32(pointers.To(7)); got != 7 {
		test.Fatalf("Int32 = %d", got)
	}
	if got := *pointers.Int(pointers.To(int32(9))); got != 9 {
		test.Fatalf("Int = %d", got)
	}
}

func TestValueDereferencesOrReturnsTheZeroValue(test *testing.T) {
	if pointers.Value[int](nil) != 0 || pointers.Value(pointers.To(7)) != 7 || pointers.Value[string](nil) != "" {
		test.Fatal("Value must dereference, and map nil to the zero value")
	}
}
