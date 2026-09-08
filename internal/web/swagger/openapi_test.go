package swagger_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/autobrr/harbrr/internal/web/swagger"
)

// specDoc is the slice of the OpenAPI document these tests assert on. Full
// OpenAPI validation is the web CI job's concern: openapi-typescript
// regenerates the client types from this same file and fails on a spec it
// cannot read.
type specDoc struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths map[string]map[string]struct{} `yaml:"paths"`
}

// loadSpec parses the embedded spec. It is the shared entry point for the
// drift tests below: malformed YAML fails here rather than in each test.
func loadSpec(t *testing.T) specDoc {
	t.Helper()

	var doc specDoc
	if err := yaml.Unmarshal(swagger.Spec(), &doc); err != nil {
		t.Fatalf("load embedded openapi.yaml: %v", err)
	}
	return doc
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

	if doc.Info.Title == "" {
		t.Error("info.title is empty")
	}
	if doc.Info.Version == "" {
		t.Error("info.version is empty")
	}

	healthz, ok := doc.Paths["/healthz"]
	if !ok {
		t.Fatal("spec does not document the /healthz path")
	}
	if _, ok := healthz["get"]; !ok {
		t.Error("/healthz does not document a GET operation")
	}
}
