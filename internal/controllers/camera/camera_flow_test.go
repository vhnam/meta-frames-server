package camera

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/common/pointers"
	"meta-frames-server/internal/db/gen"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

func newController() (*Controller, *testutil.Memory) {
	memory := testutil.NewMemory()
	all := services.New(testutil.Store{Querier: memory}, testutil.NewMemoryFiles(), clock.Fixed{})
	return New(all.Cameras), memory
}

func interchangeable() *api.CameraInput {
	return &api.CameraInput{Brand: "Nikon", Model: "FM2", Mount: pointers.To("F")}
}

func TestPutCameraCreatesThenUpdates(test *testing.T) {
	controller, _ := newController()
	id := uuid.New()
	request := api.PutCameraRequestObject{Id: id, Body: interchangeable()}

	first, err := controller.PutCamera(context.Background(), request)
	if created, ok := first.(api.PutCamera201JSONResponse); err != nil || !ok || created.Brand != "Nikon" {
		test.Fatalf("first=%#v err=%v", first, err)
	}
	second, _ := controller.PutCamera(context.Background(), request)
	if _, ok := second.(api.PutCamera200JSONResponse); !ok {
		test.Fatalf("second = %#v", second)
	}
	if _, err := controller.PutCamera(context.Background(), api.PutCameraRequestObject{Id: uuid.New(), Body: &api.CameraInput{Brand: " "}}); err == nil {
		test.Fatal("expected a validation error")
	}
}

func TestGetListAndActivateCameras(test *testing.T) {
	controller, memory := newController()
	ctx := context.Background()
	activeID, inactiveID := uuid.New(), uuid.New()
	memory.Cameras[activeID] = gen.Camera{ID: activeID, Brand: "A", IsActive: true, Mount: pointers.To("F")}
	memory.Cameras[inactiveID] = gen.Camera{ID: inactiveID, Brand: "B", Mount: pointers.To("F")}

	got, err := controller.GetCamera(ctx, api.GetCameraRequestObject{Id: activeID})
	if err != nil || got.(api.GetCamera200JSONResponse).Brand != "A" {
		test.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := controller.GetCamera(ctx, api.GetCameraRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown camera must fail")
	}

	activeOnly := true
	listed, err := controller.ListCameras(ctx, api.ListCamerasRequestObject{Params: api.ListCamerasParams{ActiveOnly: &activeOnly}})
	if err != nil || len(listed.(api.ListCameras200JSONResponse)) != 1 {
		test.Fatalf("listed=%#v err=%v", listed, err)
	}
	if every, _ := controller.ListCameras(ctx, api.ListCamerasRequestObject{}); len(every.(api.ListCameras200JSONResponse)) != 2 {
		test.Fatalf("every = %#v", every)
	}

	toggled, err := controller.SetCameraActive(ctx, api.SetCameraActiveRequestObject{Id: inactiveID, Body: &api.ActiveInput{IsActive: true}})
	if err != nil || !toggled.(api.SetCameraActive200JSONResponse).IsActive {
		test.Fatalf("toggled=%#v err=%v", toggled, err)
	}
	if _, err := controller.SetCameraActive(ctx, api.SetCameraActiveRequestObject{Id: uuid.New(), Body: &api.ActiveInput{}}); err == nil {
		test.Fatal("an unknown camera must fail")
	}
}

func TestCameraLensesCanBeLinkedAndListed(test *testing.T) {
	controller, memory := newController()
	ctx := context.Background()
	cameraID, lensID := uuid.New(), uuid.New()
	memory.Cameras[cameraID] = gen.Camera{ID: cameraID, IsActive: true, Mount: pointers.To("F")}
	memory.Lenses[lensID] = gen.Lens{ID: lensID, IsActive: true, FocalLength: 50}

	set, err := controller.SetCameraLenses(ctx, api.SetCameraLensesRequestObject{Id: cameraID, Body: &api.CameraLensesInput{LensIds: []uuid.UUID{lensID}}})
	if err != nil || len(set.(api.SetCameraLenses200JSONResponse)) != 1 {
		test.Fatalf("set=%#v err=%v", set, err)
	}
	listed, err := controller.ListCameraLenses(ctx, api.ListCameraLensesRequestObject{Id: cameraID})
	if err != nil || len(listed.(api.ListCameraLenses200JSONResponse)) != 1 {
		test.Fatalf("listed=%#v err=%v", listed, err)
	}
	if _, err := controller.ListCameraLenses(ctx, api.ListCameraLensesRequestObject{Id: uuid.New()}); err == nil {
		test.Fatal("an unknown camera must fail")
	}
	if _, err := controller.SetCameraLenses(ctx, api.SetCameraLensesRequestObject{Id: cameraID, Body: &api.CameraLensesInput{LensIds: []uuid.UUID{uuid.New()}}}); err == nil {
		test.Fatal("an unknown lens must fail")
	}
}

func TestDeleteCamera(test *testing.T) {
	controller, memory := newController()
	ctx := context.Background()
	id := uuid.New()
	memory.Cameras[id] = gen.Camera{ID: id}

	response, err := controller.DeleteCamera(ctx, api.DeleteCameraRequestObject{Id: id})
	if _, ok := response.(api.DeleteCamera204Response); err != nil || !ok || !memory.Cameras[id].DeletedAt.Valid {
		test.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := controller.DeleteCamera(ctx, api.DeleteCameraRequestObject{Id: id}); err == nil {
		test.Fatal("deleting a missing camera must fail")
	}
}

func TestCreateCameraAssignsAFreshID(test *testing.T) {
	controller, memory := newController()
	ctx := context.Background()

	first, err := controller.CreateCamera(ctx, api.CreateCameraRequestObject{Body: interchangeable()})
	if err != nil {
		test.Fatal(err)
	}
	second, _ := controller.CreateCamera(ctx, api.CreateCameraRequestObject{Body: interchangeable()})
	firstID, secondID := first.(api.CreateCamera201JSONResponse).Id, second.(api.CreateCamera201JSONResponse).Id
	if firstID == uuid.Nil || firstID == secondID || len(memory.Cameras) != 2 {
		test.Fatalf("ids = %v / %v, cameras = %d", firstID, secondID, len(memory.Cameras))
	}
	if _, err := controller.CreateCamera(ctx, api.CreateCameraRequestObject{Body: &api.CameraInput{Brand: " "}}); err == nil {
		test.Fatal("expected a validation error")
	}
}
