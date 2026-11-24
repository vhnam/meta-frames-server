package processing

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/testutil"
)

type (
	memoryQueries = testutil.Memory
	memoryFiles   = testutil.MemoryFiles
)

var (
	assertAppError = testutil.AssertAppError
	day            = testutil.Day
)

var errBoom = errors.New("boom")

func newMemoryQueries() *memoryQueries {
	queries := testutil.NewMemory()
	queries.Users[uuid.Nil] = gen.AppUser{ID: uuid.Nil, CurrencyCode: "VND"} // the owner of a bare context
	return queries
}

func newMemoryFiles() *memoryFiles { return testutil.NewMemoryFiles() }

func fixedClock() clock.Fixed { return clock.Fixed{Date: day(2026, time.October, 2)} }

func int32Ref(value int32) *int32 { return &value }

func stringRef(value string) *string { return &value }
