package swagger

import (
	"strings"
	"testing"
)

// TestUIRendersSpec asserts the embedded Swagger UI page is substituted to load the
// management-API spec and pins its (SRI-protected) asset references.
func TestUIRendersSpec(t *testing.T) {
	t.Parallel()
	page := string(UI())
	if page == "" {
		t.Fatal("UI() returned an empty page")
	}
	if strings.Contains(page, "{{OPENAPI_URL}}") {
		t.Error("UI() left the {{OPENAPI_URL}} placeholder unsubstituted")
	}
	// The spec URL is relative (a sibling of /api/docs) so it works under any base path.
	if !strings.Contains(page, `url: "openapi.yaml"`) {
		t.Error("UI() does not point at the relative openapi.yaml spec")
	}
	// Pinned swagger-ui-dist assets with SRI integrity + CORS (so SRI is enforced).
	for _, want := range []string{
		"swagger-ui-dist@5.32.6/swagger-ui.css",
		"swagger-ui-dist@5.32.6/swagger-ui-bundle.js",
		"swagger-ui-dist@5.32.6/swagger-ui-standalone-preset.js",
		`integrity="sha384-`,
		`crossorigin="anonymous"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("UI() page is missing %q", want)
		}
	}
}

// TestUIReturnsACopy proves UI() hands back a copy, so a caller cannot mutate the
// embedded page (mirroring Spec()).
func TestUIReturnsACopy(t *testing.T) {
	t.Parallel()
	a := UI()
	if len(a) == 0 {
		t.Fatal("UI() returned no bytes")
	}
	a[0] = 'X'
	if UI()[0] == 'X' {
		t.Error("UI() returned a mutable view of the embedded page")
	}
}
