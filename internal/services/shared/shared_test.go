package shared

import (
	"errors"
	"meta-frames-server/internal/common/apperror"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRemoveDuplicatesKeepsFirstOccurrenceOrder(test *testing.T) {
	first, second := uuid.New(), uuid.New()
	got := RemoveDuplicates([]uuid.UUID{first, second, first, second, first})
	if len(got) != 2 || got[0] != first || got[1] != second {
		test.Fatalf("RemoveDuplicates = %v", got)
	}
}

func TestDateOrDefault(test *testing.T) {
	fallback, given := testutil.Day(2026, time.May, 1), testutil.Day(2025, time.January, 2)
	if got := DateOrDefault(nil, fallback); !got.Equal(fallback) {
		test.Fatalf("nil date = %v", got)
	}
	if got := DateOrDefault(&given, fallback); !got.Equal(given) {
		test.Fatalf("given date = %v", got)
	}
}

func TestNotFoundOrTranslatesOnlyNoRows(test *testing.T) {
	testutil.AssertAppError(test, NotFoundOr(pgx.ErrNoRows, "camera"), apperror.KindNotFound, "not_found")
	other := errors.New("boom")
	if got := NotFoundOr(other, "camera"); !errors.Is(got, other) {
		test.Fatalf("other errors pass through, got %v", got)
	}
	if NotFoundOr(nil, "camera") != nil {
		test.Fatal("nil stays nil")
	}
}

func TestLensIDSetIndexesLensesByID(test *testing.T) {
	first, second := uuid.New(), uuid.New()
	set := LensIDSet([]gen.Lens{{ID: first}, {ID: second}})
	if len(set) != 2 || !set[first] || !set[second] || set[uuid.New()] {
		test.Fatalf("set = %v", set)
	}
	if len(LensIDSet(nil)) != 0 {
		test.Fatal("no lenses, empty set")
	}
}
