package requestctx

import (
	"context"
	"testing"
)

func TestMetaRoundTripsThroughTheContext(test *testing.T) {
	if _, ok := From(context.Background()); ok {
		test.Fatal("an empty context carries no meta")
	}
	meta := Meta{RequestID: "req-1", Actor: "nam"}
	got, ok := From(With(context.Background(), meta))
	if !ok || got != meta {
		test.Fatalf("got %+v ok=%v", got, ok)
	}
}
