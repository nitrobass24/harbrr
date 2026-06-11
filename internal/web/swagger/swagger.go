// Package swagger owns seekbrr's hand-authored management-API OpenAPI spec,
// embedded into the binary. Serving (chi handler + Swagger UI) is wired later
// with the web server; this package owns the spec and its drift test.
package swagger

import (
	"bytes"
	_ "embed"
)

//go:embed openapi.yaml
var openapiYAML []byte

// Spec returns the embedded OpenAPI document as raw YAML bytes. The returned
// slice is a copy, so callers cannot mutate the embedded spec.
func Spec() []byte {
	return bytes.Clone(openapiYAML)
}
