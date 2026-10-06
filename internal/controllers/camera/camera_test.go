package camera

import (
	"meta-frames-server/internal/api"
	"meta-frames-server/internal/db/gen"
	camerasvc "meta-frames-server/internal/services/camera"
	"testing"
	"time"

	"github.com/google/uuid"
)

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func text(value string) *string { return &value }

func TestToAPICameraIncludesTheLoadedRoll(test *testing.T) {
	builtInLensID := uuid.New()
	started := date(2026, time.September, 1)
	view := camerasvc.View{
		Camera:        gen.Camera{ID: uuid.New(), Brand: "Canon", Model: "QL17", HasFixedLens: true, IsActive: true},
		BuiltInLensID: &builtInLensID,
		LoadedRoll:    &camerasvc.LoadedRoll{StockBrand: "Kodak", StockName: "Gold", ShotISO: 800, StartedAt: &started, DaysLoaded: 31},
	}
	camera := toAPICamera(view)

	if camera.Brand != "Canon" || camera.BuiltInLensId == nil || *camera.BuiltInLensId != builtInLensID {
		test.Fatalf("camera = %+v", camera)
	}
	loaded := camera.LoadedRoll
	if loaded == nil || *loaded.ShotIso != 800 || loaded.DaysLoaded != 31 || loaded.StockName != "Gold" || loaded.StartedAt == nil {
		test.Fatalf("loaded roll = %+v", loaded)
	}
	if toAPICamera(camerasvc.View{}).LoadedRoll != nil {
		test.Fatal("an unloaded camera must have no loaded roll")
	}
}

func TestToCameraInputCopiesTheFixedLens(test *testing.T) {
	body := &api.CameraInput{
		Brand: "Canon", Model: "QL17", HasFixedLens: true,
		FixedLens: &api.FixedLensInput{FocalLength: 40, MaxAperture: 1.7, Brand: text("Canon")},
	}
	input := toCameraInput(body)
	if input.FixedLens == nil || input.FixedLens.FocalLength != 40 || input.FixedLens.MaxAperture != 1.7 || *input.FixedLens.Brand != "Canon" {
		test.Fatalf("input = %+v", input)
	}
	if toCameraInput(&api.CameraInput{Brand: "a", Model: "b"}).FixedLens != nil {
		test.Fatal("no fixed lens expected")
	}
}
