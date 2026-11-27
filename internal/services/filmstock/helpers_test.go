package filmstock

import "meta-frames-server/internal/testutil"

type memoryQueries = testutil.Memory

var assertAppError = testutil.AssertAppError

func newMemoryQueries() *memoryQueries { return testutil.NewMemory() }

func int32Ref(value int32) *int32 { return &value }

func stringRef(value string) *string { return &value }
