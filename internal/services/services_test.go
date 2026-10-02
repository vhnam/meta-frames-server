package services

import (
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/testutil"
	"testing"
)

func TestServicesNewWiresEveryService(test *testing.T) {
	all := New(testutil.Store{Querier: testutil.NewMemory()}, testutil.NewMemoryFiles(), clock.Fixed{})
	if all.Cameras == nil || all.Lenses == nil || all.FilmStocks == nil || all.Rolls == nil || all.Labs == nil ||
		all.Processing == nil || all.Scans == nil || all.Stats == nil {
		test.Fatalf("services = %+v", all)
	}
}
