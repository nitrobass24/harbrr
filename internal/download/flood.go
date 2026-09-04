package download

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// floodDriver is a thin HTTP client for Flood (jesec/flood), a torrent-only web
// UI authenticated by a username/password login that returns an httpOnly jwt
// cookie. No cookiejar: the jwt is written into the shared client's auth header
// after each login, which is simpler than a full cookiejar for a single cookie.
type floodDriver struct {
	username    string
	password    string
	destination string
	tags        []string
	startPaused bool
	jc          *apphttp.JSONClient
	// authed reports whether jc.Auth carries a jwt cookie from a successful login.
	authed bool
}

// newFlood builds the Flood driver from a configured client row and its
// decrypted secret (the account password).
func newFlood(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.FloodSettings
	if c.Settings.Flood != nil {
		settings = *c.Settings.Flood
	}
	return &floodDriver{
		username:    c.Username,
		password:    secret,
		destination: settings.Destination,
		tags:        settings.Tags,
		startPaused: settings.StartPaused,
		jc: apphttp.NewJSONClient(apphttp.JSONClient{
			Prefix: "download: flood",
			Base:   c.Host,
			Auth:   http.Header{},
			Client: client,
			Secret: secret,
		}),
	}, nil
}

// Test confirms the login succeeds and the client-connection-test endpoint is
// reachable.
func (d *floodDriver) Test(ctx context.Context) error {
	return d.do(ctx, http.MethodGet, "/api/client/connection-test", nil)
}

// Add posts a torrent (magnet/http URL or raw bytes) to Flood's add-urls or
// add-files endpoint. Torrent-only — Flood has no usenet client. Flood has no
// category concept, so opts.Category is folded into the tag set. `start` is
// always sent explicitly (Flood's default is false = added stopped).
func (d *floodDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent {
		return fmt.Errorf("download: flood: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}

	tags := mergeTags(d.tags, opts.Tags)
	if opts.Category != "" {
		tags = mergeTags(tags, []string{opts.Category})
	}
	start := !d.startPaused && !opts.Paused

	payload := struct {
		URLs        []string `json:"urls,omitempty"`
		Files       []string `json:"files,omitempty"`
		Destination string   `json:"destination,omitempty"`
		Tags        []string `json:"tags,omitempty"`
		Start       bool     `json:"start"`
	}{Destination: d.destination, Tags: tags, Start: start}

	path := "/api/torrents/add-urls"
	if len(p.Bytes) > 0 {
		path = "/api/torrents/add-files"
		payload.Files = []string{base64.StdEncoding.EncodeToString(p.Bytes)}
	} else {
		payload.URLs = []string{p.URL}
	}

	return d.do(ctx, http.MethodPost, path, payload)
}

// authenticate logs in and stores the returned jwt cookie value.
func (d *floodDriver) authenticate(ctx context.Context) error {
	body, err := json.Marshal(struct { //nolint:gosec // G117: this IS the credential — it's the auth request body sent to Flood's own login endpoint, never logged.
		Username string `json:"username"`
		Password string `json:"password"`
	}{d.username, d.password})
	if err != nil {
		return fmt.Errorf("download: flood: encode auth request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.jc.Base+"/api/auth/authenticate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("download: flood: build auth request: %w", apphttp.RedactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.jc.Client.Do(req)
	if err != nil {
		return fmt.Errorf("download: flood: authenticate: %w", apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: flood: authenticate: unexpected status %d", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "jwt" {
			// The jwt rides every later request as the client's auth header; Flood has
			// no other credential to send.
			d.jc.Auth.Set("Cookie", "jwt="+ck.Value)
			d.authed = true
			return nil
		}
	}
	return errors.New("download: flood: authenticate: no jwt cookie returned")
}

// do issues an authenticated request, authenticating first if no session is
// cached yet, and re-authenticating exactly once on a 401 before retrying.
func (d *floodDriver) do(ctx context.Context, method, path string, in any) error {
	if !d.authed {
		if err := d.authenticate(ctx); err != nil {
			return err
		}
	}
	status, err := d.jc.Do(ctx, method, path, in, nil)
	if status != http.StatusUnauthorized {
		return err
	}
	if err := d.authenticate(ctx); err != nil {
		return err
	}
	_, err = d.jc.Do(ctx, method, path, in, nil)
	return err
}
