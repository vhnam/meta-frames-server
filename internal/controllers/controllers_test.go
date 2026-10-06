package controllers

import (
	"context"
	"testing"

	"meta-frames-server/internal/api"
)

func TestHealthControllerReportsOk(test *testing.T) {
	response, err := (&HealthController{}).GetHealth(context.Background(), api.GetHealthRequestObject{})
	if err != nil {
		test.Fatal(err)
	}
	if got := response.(api.GetHealth200JSONResponse).Status; got != api.Ok {
		test.Fatalf("status = %q", got)
	}
}
