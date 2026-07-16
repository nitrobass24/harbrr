// Package avistaz is the native driver for the AvistaZ network (AvistaZ, CinemaZ,
// PrivateHD, ExoticaZ). These have no Cardigann definition because their
// login→Bearer `api/v1/jackett` auth exceeds the declarative format, so the
// search/parse/grab logic lives here in Go. The driver reproduces Prowlarr's (and
// Jackett's) documented contract and reuses every harbrr seam (paced HTTP client,
// secret store, normalized release, caps mapper, the /dl grab proxy, redaction).
package avistaz

import (
	"context"
	"sync"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured AvistaZ-family instance. It is built once per instance
// and cached by the registry; the bearer token is held per driver and refreshed
// reactively on a 401/412.
type driver struct {
	native.Base
	profile profile

	mu    sync.Mutex
	token string // cached bearer; refreshed reactively
}

var _ native.Driver = (*driver)(nil)

// profile captures the per-site behaviour that differs across the four families,
// keyed off the definition id: AvistaZ renders a seasonless episode as "E{n}";
// ExoticaZ derives categories from the response `category` dict (not type+quality).
type profile struct {
	site            string
	episodeOverride bool
	exoticaParse    bool
}

func profileFor(id string) profile {
	return profile{
		site:            id,
		episodeOverride: id == "avistaz",
		exoticaParse:    id == "exoticaz",
	}
}

// New is the native.Factory for every AvistaZ-family site. It builds the
// capabilities from the (per-site) definition and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	base, err := native.NewBase("avistaz", p)
	if err != nil {
		return nil, err
	}
	return &driver{
		Base:    base,
		profile: profileFor(p.Def.ID),
	}, nil
}

// NeedsResolver is always true: an AvistaZ download URL must be fetched with the
// Bearer header *arr cannot send, so the served feed routes through the /dl proxy
// and the driver's Grab fetches the torrent server-side.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false: AvistaZ is already routed through /dl by NeedsResolver,
// so the out-of-band-auth signal would be redundant.
func (d *driver) DownloadNeedsAuth() bool { return false }

// Test verifies the configured credentials authenticate (the management
// "test indexer" action). It forces a fresh token fetch.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.refreshToken(ctx)
	return err
}
