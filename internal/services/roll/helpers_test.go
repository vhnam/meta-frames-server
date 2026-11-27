package roll

import (
	"time"

	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/testutil"
)

type memoryQueries = testutil.Memory

var (
	assertAppError = testutil.AssertAppError
	day            = testutil.Day
)

func newMemoryQueries() *memoryQueries { return testutil.NewMemory() }

func fixedClock() clock.Fixed { return clock.Fixed{Date: day(2026, time.October, 2)} }

func int32Ref(value int32) *int32 { return &value }

func stringRef(value string) *string { return &value }
