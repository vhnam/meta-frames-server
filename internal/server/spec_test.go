package server

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"meta-frames-server/internal/api"
)

func TestSpecIsValidIncludingExamples(test *testing.T) {
	swagger, err := api.GetSwagger()
	if err != nil {
		test.Fatal(err)
	}
	if err := swagger.Validate(context.Background(), openapi3.EnableExamplesValidation()); err != nil {
		test.Fatal(err)
	}
}
