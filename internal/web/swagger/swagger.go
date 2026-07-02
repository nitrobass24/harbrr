// Package swagger owns harbrr's hand-authored management-API OpenAPI spec and the
// embedded Swagger UI page that renders it, both compiled into the binary. The spec
// drives the drift test and is served at /api/openapi.yaml; the UI page is served at
// /api/docs by the web server.
package swagger

import (
	"bytes"
	_ "embed"
	"strings"
)

//go:embed openapi.yaml
var openapiYAML []byte

//go:embed index.html
var swaggerHTML string

// swaggerUIPage is the Swagger UI page with the spec URL substituted once. The spec
// reference is relative ("openapi.yaml", a sibling of /api/docs) so the page resolves
// the spec correctly under any base path, with no server-side rewriting.
var swaggerUIPage = []byte(strings.ReplaceAll(swaggerHTML, "{{OPENAPI_URL}}", "openapi.yaml"))

// Spec returns the embedded OpenAPI document as raw YAML bytes. The returned slice is
// a copy, so callers cannot mutate the embedded spec.
func Spec() []byte {
	return bytes.Clone(openapiYAML)
}

// UI returns the embedded Swagger UI page (HTML) that renders the spec at
// /api/openapi.yaml. The returned slice is a copy.
func UI() []byte {
	return bytes.Clone(swaggerUIPage)
}
