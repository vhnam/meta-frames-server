package processing

import (
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

type memoryQueries = testutil.Memory

var (
	assertAppError = testutil.AssertAppError
	day            = testutil.Day
)

func newMemoryQueries() *memoryQueries {
	queries := testutil.NewMemory()
	queries.Users[uuid.Nil] = gen.AppUser{ID: uuid.Nil, CurrencyCode: "VND"} // the owner of a bare context
	return queries
}

func fixedClock() clock.Fixed { return clock.Fixed{Date: day(2026, time.October, 2)} }
