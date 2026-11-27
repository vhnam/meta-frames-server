package lab

import (
	"errors"

	"meta-frames-server/internal/testutil"
)

var assertAppError = testutil.AssertAppError

var errBoom = errors.New("boom")
