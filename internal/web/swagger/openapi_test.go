package swagger_test

import (
	"context"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/autobrr/harbrr/internal/web/swagger"
)

// loadSpec parses and validates the embedded spec as an OpenAPI 3 document. It
// is the shared entry point for the drift tests below: anything malformed (bad
// YAML or an invalid OpenAPI 3 structure) fails here rather than in each test.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(swagger.Spec())
	if err != nil {
		t.Fatalf("load embedded openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("embedded spec is not valid OpenAPI 3: %v", err)
	}
	return doc
}

func TestSpecIsValidOpenAPI3(t *testing.T) {
	t.Parallel()

	// loadSpec does the parse + Validate; reaching here means the embedded
	// spec is a structurally valid OpenAPI 3 document.
	loadSpec(t)
}

func TestSpecContract(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)

	tests := []struct {
		name string
		got  func() string
		want string // exact match, or prefix when wantPrefix is set
	}{
		{name: "openapi version", got: func() string { return doc.OpenAPI }, want: "3.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.got(); !strings.HasPrefix(got, tt.want) {
				t.Errorf("%s = %q, want prefix %q", tt.name, got, tt.want)
			}
		})
	}

	if doc.Info == nil {
		t.Fatal("spec is missing the info block")
		return
	}
	if doc.Info.Title == "" {
		t.Error("info.title is empty")
	}
	if doc.Info.Version == "" {
		t.Error("info.version is empty")
	}

	healthz := doc.Paths.Find("/healthz")
	if healthz == nil {
		t.Fatal("spec does not document the /healthz path")
		return
	}
	if healthz.Get == nil {
		t.Error("/healthz does not document a GET operation")
	}
}
