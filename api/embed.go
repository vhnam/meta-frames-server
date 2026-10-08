// Package api embeds the OpenAPI contract. Request validation uses this full spec rather than
// the generated code's, because code generation leaves out the operations authboss serves.
package api

import (
	_ "embed"

	"github.com/getkin/kin-openapi/openapi3"
)

//go:embed openapi.yaml
var spec []byte

// Load parses the contract.
func Load() (*openapi3.T, error) {
	return openapi3.NewLoader().LoadFromData(spec)
}
