package server

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	apispec "meta-frames-server/api"
)

func TestSpecIsValidIncludingExamples(test *testing.T) {
	swagger, err := apispec.Load()
	if err != nil {
		test.Fatal(err)
	}
	// A description next to a $ref documents the field; kin-openapi ignores it when resolving.
	if err := swagger.Validate(context.Background(), openapi3.EnableExamplesValidation(), openapi3.AllowExtraSiblingFields("description")); err != nil {
		test.Fatal(err)
	}
}
