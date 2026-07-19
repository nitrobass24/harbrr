package download

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// floodDriver is a thin HTTP client for Flood (jesec/flood), a torrent-only web
// UI authenticated by a username/password login that returns an httpOnly jwt
// cookie. No cookiejar: the jwt is stored as a plain field and attached manually
// per request, which is simpler than a full cookiejar for a single cookie.
type floodDriver struct {
	host        string
	username    string
	password    string
	destination string
	tags        []string
	startPaused bool
	client      *http.Client
	jwt         string
}

// newFlood builds the Flood driver from a configured client row and its
// decrypted secret (the account password).
func newFlood(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.FloodSettings
	if c.Settings.Flood != nil {
		settings = *c.Settings.Flood
	}
	return &floodDriver{
		host:        strings.TrimRight(c.Host, "/"),
		username:    c.Username,
		password:    secret,
		destination: settings.Destination,
		tags:        settings.Tags,
		startPaused: settings.StartPaused,
		client:      client,
	}, nil
}

// Test confirms the login succeeds and the client-connection-test endpoint is
// reachable.
func (d *floodDriver) Test(ctx context.Context) error {
	resp, err := d.do(ctx, http.MethodGet, "/api/client/connection-test", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: flood: connection test: unexpected status %d", resp.StatusCode)
	}
	return nil
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

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("download: flood: encode add request: %w", err)
	}

	resp, err := d.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download: flood: add torrent: unexpected status %d: %s", resp.StatusCode, apphttp.RedactError(errors.New(string(msg))))
	}
	return nil
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

	resp, err := d.doOnce(ctx, http.MethodPost, "/api/auth/authenticate", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: flood: authenticate: unexpected status %d", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "jwt" {
			d.jwt = ck.Value
			return nil
		}
	}
	return errors.New("download: flood: authenticate: no jwt cookie returned")
}

// do issues an authenticated request, authenticating first if no session is
// cached yet, and re-authenticating exactly once on a 401 before retrying.
func (d *floodDriver) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	if d.jwt == "" {
		if err := d.authenticate(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := d.doOnce(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	resp.Body.Close()
	if err := d.authenticate(ctx); err != nil {
		return nil, err
	}
	return d.doOnce(ctx, method, path, body)
}

// doOnce builds and issues a single request, attaching the jwt cookie (empty
// before the first authenticate) and a JSON content type when a body is given.
func (d *floodDriver) doOnce(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.host+path, r)
	if err != nil {
		return nil, fmt.Errorf("download: flood: build request: %w", apphttp.RedactURLError(err))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "jwt", Value: d.jwt}) //nolint:gosec // request cookie; Set-Cookie security attrs are N/A

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: flood: %w", apphttp.RedactURLError(err))
	}
	return resp, nil
}
