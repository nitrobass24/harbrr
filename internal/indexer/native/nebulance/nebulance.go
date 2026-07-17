// Package nebulance implements the native Nebulance (NBL) JSON API driver. It has no
// Cardigann definition because its query-string API-key auth and page/per_page paged
// JSON envelope are simple enough to build in Go directly; the driver reuses every
// harbrr seam (paced HTTP client, secret store, normalized release, caps mapper, the
// /dl grab proxy, redaction) via the embedded native.Base.
package nebulance

import (
	"context"
	"errors"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	defaultBaseURL      = "https://nebulance.io/"
	requestDelaySeconds = 2.0
)

// driver is one configured Nebulance instance. There is no login round-trip: every
// request carries the API key in its query, so the driver holds no session state.
// Everything but the NBL request/parse dialect lives in the embedded native.Base.
type driver struct {
	native.Base
}

var (
	_                 native.Driver = (*driver)(nil)
	errAPIKeyRequired               = errors.New("nebulance: API key is required")
)

// authClassify is NBL's status dialect: 401/403 are auth failures (429/503 are always
// rate-limit statuses via search.IsRateLimitStatus, applied unconditionally by
// Classify.statusError). The custom reason names the actionable fix for a
// non-interactive caller (*arr) that cannot re-authenticate interactively.
var authClassify = native.ClassifyAuth403.WithAuthReason(
	"unauthorized in non-interactive mode; verify or replace the configured API key",
)

// Families returns Nebulance's native family registration.
func Families() []native.Family {
	return []native.Family{{Definition: Definition(), Factory: New}}
}

// Definition returns Nebulance's static settings and capabilities definition.
func Definition() *loader.Definition {
	delay := requestDelaySeconds
	allowRaw := true
	allowIMDB := true
	return &loader.Definition{
		ID:           "nebulance",
		Name:         "Nebulance",
		Description:  "Nebulance (NBL) — ratioless private TV tracker (native driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{defaultBaseURL},
		RequestDelay: &delay,
		Settings: []loader.SettingsField{
			{Name: "apikey", Label: "API Key", Type: "text", Required: true},
		},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				categoryMapping("1", "TV", "TV"),
				categoryMapping("2", "SD", "TV/SD"),
				categoryMapping("3", "HD", "TV/HD"),
				categoryMapping("4", "UHD", "TV/UHD"),
			},
			Modes: loader.Modes{
				Search:   []string{"q"},
				TVSearch: []string{"q", "season", "ep", "imdbid", "tvmazeid"},
			},
			AllowRawSearch:    &allowRaw,
			AllowTVSearchIMDB: &allowIMDB,
		},
	}
}

func categoryMapping(id, desc, category string) loader.CategoryMapping {
	return loader.CategoryMapping{
		ID:   loader.Scalar{Value: id, Set: true},
		Cat:  category,
		Desc: desc,
	}
}

// New builds one Nebulance driver from decrypted instance settings. It rejects a nil
// definition (via native.NewBase) or a missing API key.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("nebulance", p)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(b.Cfg["apikey"]) == "" {
		return nil, errAPIKeyRequired
	}
	return &driver{Base: b}, nil
}

// NeedsResolver hides NBL's token-bearing download URL behind harbrr's /dl endpoint.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false because the returned download URL carries its own token.
func (d *driver) DownloadNeedsAuth() bool { return false }

// SupportsOffsetPaging reports that NBL accepts page and per_page upstream.
func (d *driver) SupportsOffsetPaging() bool { return true }

// Test verifies the configured API key with a one-result browse request. It returns
// [login.ErrLoginFailed] when Nebulance rejects the credentials.
func (d *driver) Test(ctx context.Context) error {
	if strings.TrimSpace(d.Cfg["apikey"]) == "" {
		return errAPIKeyRequired
	}
	_, err := d.Search(ctx, search.Query{Limit: 1})
	return err
}
