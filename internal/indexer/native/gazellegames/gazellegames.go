// Package gazellegames is the native driver for GazelleGames (gazellegames.net), a
// Gazelle-derived games/applications tracker. It has no Cardigann definition because its
// api.php JSON endpoint — an API key carried in the X-API-Key header, numerics
// wire-encoded as JSON strings, a nested group→torrents structure that flattens to one
// release per torrent, and a download URL rebuilt from a server-fetched passkey
// (torrents.php?action=download&torrent_pass=…) — exceeds the declarative format, so the
// search/parse/grab logic lives here in Go. The driver reproduces Prowlarr's documented
// contract (GazelleGames / GazelleGamesRequestGenerator / GazelleGamesParser) and reuses
// every harbrr seam (paced HTTP client, secret store, normalized release, caps mapper,
// the /dl grab proxy, redaction).
package gazellegames

import (
	"context"
	"strings"
	"sync"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured GazelleGames instance. It is built once per instance and
// cached by the registry. There is no login round-trip: every request carries the API
// key in the X-API-Key header, so the driver holds no session state. The download
// passkey is fetched on demand (request=quick_user) and persisted via persist.
type driver struct {
	native.Base
	persist func(ctx context.Context, name, value string) error

	// mu guards currentPasskey.
	mu sync.Mutex
	// currentPasskey is the download passkey, seeded from the settings and refreshed
	// on demand (request=quick_user). It lives HERE and not in Cfg because Cfg is the
	// registry-owned settings map, shared with readers outside this driver that do not
	// take d.mu — writing into it would be a concurrent map write.
	currentPasskey string
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for GazelleGames. It builds the capabilities from the
// definition, normalises the base URL, and defaults the clock.
func New(p native.Params) (native.Driver, error) {
	if p.Cfg == nil {
		p.Cfg = map[string]string{}
	}
	b, err := native.NewBase("gazellegames", p)
	if err != nil {
		return nil, err
	}
	return &driver{
		Base:           b,
		persist:        p.PersistSetting,
		currentPasskey: strings.TrimSpace(p.Cfg["passkey"]),
	}, nil
}

// cfgValue reads a config value. Cfg is wired once by NewBase and never written by this
// driver (the on-demand passkey lives in currentPasskey), so this is Base's ordinary
// read-only contract.
func (d *driver) cfgValue(name string) string {
	return d.Cfg[name]
}

// passkey reads the current download passkey under the mutex it shares with
// storePasskey's writer.
func (d *driver) passkey() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.currentPasskey
}

// scrub is GazelleGames' own value-scrub, NOT native.Base.Scrub. It shares the same
// two primitives (loader.SecretValues + apphttp.ScrubValues) but cannot go through
// Base.Scrub directly: the download passkey is not a declared Settings field at all
// (see sites.go's Families: "The download passkey is NOT a user setting"), so
// loader.SecretValues could never see it via Cfg/Settings — it must be passed
// explicitly, and the on-demand value is read under d.mu.
//
// The derivation runs over the FULL Cfg, so a future secret setting added to the
// declared Settings is picked up automatically rather than silently missed by a
// hand-built key list.
func (d *driver) scrub(s string) string {
	secrets := loader.SecretValues(d.Def.Settings, d.Cfg)
	return apphttp.ScrubValues(s, append(secrets, d.passkey()))
}

// NeedsResolver is always true: a GazelleGames download URL carries the passkey in its
// torrent_pass query param, which *arr must not see, so the served feed routes through
// the /dl proxy and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: GazelleGames is already routed through /dl by NeedsResolver,
// so the out-of-band-auth signal would be redundant (it mirrors FileList).
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured API key authenticates and fetches the download passkey (the
// management "test indexer" action). It fetches the passkey first (Prowlarr's Test calls
// FetchPasskey before the base probe) so a misconfigured key surfaces immediately and the
// passkey is persisted for later downloads, then issues a cheap latest-torrents search. A
// 401/403 from either step surfaces as login.ErrLoginFailed so the registry records an
// auth_failure health event; neither the apikey nor the passkey is ever logged.
func (d *driver) Test(ctx context.Context) error {
	if err := d.fetchPasskey(ctx); err != nil {
		return err
	}
	_, err := d.Search(ctx, search.Query{})
	return err
}
