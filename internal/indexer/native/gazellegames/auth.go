package gazellegames

import (
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// apiKeyHeader is the GGn auth header. The api.php endpoint authenticates every request by
// the X-API-Key header (confirmed in autobrr ggn.go and Prowlarr GazelleGames); the value
// is the secret and MUST NEVER be logged.
const apiKeyHeader = "X-API-Key" //nolint:gosec // header NAME, not a credential value

// get issues an authenticated GET to a GGn endpoint (api.php search or a torrents.php
// download). The API key rides in the X-API-Key header — never in the URL and never logged
// — so the header is set but never recorded; Accept advertises JSON. The download URL
// (torrents.php) carries the passkey in its torrent_pass query, so a transport error
// surfaces only its scheme://host through native.Base, so a passkey-bearing download URL
// can never leak.
func (d *driver) get(ctx context.Context, rawurl string, download bool) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("gazellegames: build request: %w", err)
	}
	req.Header.Set(apiKeyHeader, strings.TrimSpace(d.cfgValue("apikey")))
	req.Header.Set("Accept", "application/json")
	if download {
		return d.DoDownload(ctx, req, native.ClassifyAuth403)
	}
	return d.Do(ctx, req, native.ClassifyAuth403)
}

// scrubSecrets removes the configured apikey (and any persisted passkey) from s so a
// transport/server message echo cannot leak a secret. It mirrors scrubAPIKey but also
// covers the download passkey, which the X-API-Key header never carries but a rebuilt URL
// could surface.
func (d *driver) scrubSecrets(s string) string {
	s = d.scrubAPIKey(s)
	if pass := strings.TrimSpace(d.cfgValue("passkey")); pass != "" {
		s = strings.ReplaceAll(s, pass, "[redacted]")
	}
	return s
}

// quickUserParam is the api.php request that returns the authenticated user's profile,
// including the download passkey (Prowlarr's FetchPasskey).
const quickUserParam = "quick_user"

// gazelleGamesUserResponse is the api.php?request=quick_user envelope. Status is "success"
// on a good response; Response.Passkey carries the download passkey (a secret).
type gazelleGamesUserResponse struct {
	Status   flexString `json:"status"`
	Response struct {
		Passkey string `json:"passkey"`
	} `json:"response"`
}

// ensurePasskey fetches and persists the download passkey if it is not already configured.
// The passkey is required to build a working torrents.php download URL (torrent_pass); GGn
// exposes it via request=quick_user rather than as a user setting, so it is fetched on
// demand (Prowlarr fetches it in Test and keeps it on Settings). A configured passkey is
// reused without a round-trip.
func (d *driver) ensurePasskey(ctx context.Context) error {
	if strings.TrimSpace(d.cfgValue("passkey")) != "" {
		return nil
	}
	return d.fetchPasskey(ctx)
}

// fetchPasskey issues the authenticated api.php?request=quick_user call, reads the passkey
// from the response, stores it in cfg, and persists it via the registry so it survives a
// restart (mirroring Prowlarr's FetchPasskey). A non-success status or an empty passkey is
// an auth failure (login.ErrLoginFailed). The passkey is a secret: it is never logged, and
// any surfaced error is scrubbed of both the apikey and the passkey.
func (d *driver) fetchPasskey(ctx context.Context) error {
	resp, err := d.get(ctx, d.quickUserURL(), false)
	if err != nil {
		return err
	}
	return d.storePasskey(ctx, resp.Body)
}

// storePasskey decodes a quick_user body and, on a success status with a non-empty passkey,
// records it in cfg and persists it. A malformed body, a non-success status, or an empty
// passkey is an auth failure (login.ErrLoginFailed) — without it no working download URL can
// be built. The passkey is never logged.
func (d *driver) storePasskey(ctx context.Context, body []byte) error {
	var resp gazelleGamesUserResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("gazellegames: decode passkey response: %w", login.ErrLoginFailed)
	}
	passkey := strings.TrimSpace(resp.Response.Passkey)
	if resp.Status.string() != statusSuccess || passkey == "" {
		// resp.Status is the SERVER-CONTROLLED JSON `status` field (arbitrary server text,
		// not an HTTP status), and the apikey rode in the X-API-Key header on this same
		// request, so scrub both apikey and passkey out of any echoed status before it
		// reaches a persisted health event / webhook (mirrors hdbits/beyondhd).
		return fmt.Errorf("gazellegames: passkey fetch failed (status %q): %w", d.scrubSecrets(resp.Status.string()), login.ErrLoginFailed)
	}

	// Persist FIRST, then populate the in-memory cfg only on success. If persist fails,
	// d.Cfg["passkey"] stays empty so ensurePasskey will retry on the next search rather
	// than serving a passkey the store never recorded (live/stored must not diverge).
	d.mu.Lock()
	persist := d.persist
	d.mu.Unlock()

	if persist != nil {
		if err := persist(ctx, "passkey", passkey); err != nil {
			return fmt.Errorf("gazellegames: persist passkey: %w", err)
		}
	}

	d.mu.Lock()
	d.Cfg["passkey"] = passkey
	d.mu.Unlock()
	return nil
}

// quickUserURL builds the api.php?request=quick_user URL. It carries no secret (auth is the
// X-API-Key header), so it is safe to log.
func (d *driver) quickUserURL() string {
	params := url.Values{}
	params.Set("request", quickUserParam)
	return d.BaseURL + searchPath + "?" + params.Encode()
}
