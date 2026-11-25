package lens

import "meta-frames-server/internal/testutil"

type memoryQueries = testutil.Memory

var assertAppError = testutil.AssertAppError

func newMemoryQueries() *memoryQueries { return testutil.NewMemory() }

func stringRef(value string) *string { return &value }
