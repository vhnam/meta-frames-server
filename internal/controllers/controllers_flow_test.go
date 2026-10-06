package controllers

import (
	"testing"

	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

func TestNewWiresEveryAreaIntoTheServerInterface(test *testing.T) {
	appServices := services.New(testutil.Store{Querier: testutil.NewMemory()}, testutil.NewMemoryFiles(), clock.Fixed{})
	all := New(appServices)
	if all.HealthController == nil || all.cameraController == nil || all.lensController == nil || all.filmStockController == nil ||
		all.rollController == nil || all.labController == nil || all.processingController == nil ||
		all.scanController == nil || all.statsController == nil {
		test.Fatalf("a controller is missing: %+v", all)
	}
}
