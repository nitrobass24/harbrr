package avistaz

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const testBaseURL = "https://az.test/"

func testDriver(t *testing.T, site, baseURL string, cfg map[string]string, doer search.Doer) *driver {
	t.Helper()
	if cfg == nil {
		cfg = map[string]string{}
	}
	def := testDefinition(t, site)
	d, err := New(native.Params{
		Def:     def,
		Cfg:     cfg,
		Doer:    doer,
		BaseURL: baseURL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

func testDefinition(t *testing.T, site string) *loader.Definition {
	t.Helper()
	for _, fam := range Families() {
		if fam.Definition.ID == site {
			return fam.Definition
		}
	}
	t.Fatalf("unknown avistaz test site %q", site)
	return nil
}
