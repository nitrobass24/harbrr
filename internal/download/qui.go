package download

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// quiDriver is a thin HTTP client for qui (github.com/autobrr/qui), a
// multi-instance qBittorrent manager authenticated with a static X-API-Key. No
// go-qui module and no reuse of internal/announce's qui cross-seed client — this
// talks a different API surface (per-instance torrent add, not the webhook
// check/apply pair) and stays self-contained (#8, #242).
type quiDriver struct {
	jc         *apphttp.JSONClient
	instanceID int
	category   string
	tags       []string
	paused     bool
}

// newQui builds the qui driver from a configured client row and its decrypted
// secret (the API key). InstanceID > 0 is enforced by the download service at
// Create/Update time (validateSettings), not here.
func newQui(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.QuiSettings
	if c.Settings.Qui != nil {
		settings = *c.Settings.Qui
	}
	return &quiDriver{
		jc: apphttp.NewJSONClient(apphttp.JSONClient{
			Prefix: "download: qui",
			Base:   c.Host,
			Auth:   http.Header{"X-API-Key": {secret}},
			Client: client,
			Secret: secret,
		}),
		instanceID: settings.InstanceID,
		category:   settings.Category,
		tags:       settings.Tags,
		paused:     settings.StartPaused,
	}, nil
}

// quiInstance is the subset of qui's GET /api/instances response Test reads.
type quiInstance struct {
	ID int `json:"id"`
}

// Test confirms the API key is valid and the configured instance id exists.
func (d *quiDriver) Test(ctx context.Context) error {
	var instances []quiInstance
	if _, err := d.jc.Do(ctx, http.MethodGet, "/api/instances", nil, &instances); err != nil {
		return err
	}
	for _, inst := range instances {
		if inst.ID == d.instanceID {
			return nil
		}
	}
	return fmt.Errorf("download: qui: instance %d not found", d.instanceID)
}

// Add posts a torrent (magnet/http URL or raw bytes) to qui's per-instance
// torrents endpoint. Torrent-only — qui has no usenet client to hand a payload
// to. Never emits ratioLimit/seedingTimeLimit: harbrr does not hit-and-run a
// client-managed torrent (the qBittorrent driver's #246/no-hit-and-run
// precedent).
func (d *quiDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent {
		return fmt.Errorf("download: qui: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}

	category := d.category
	if opts.Category != "" {
		category = opts.Category
	}
	tags := mergeTags(d.tags, opts.Tags)
	paused := d.paused || opts.Paused

	body, contentType, err := quiAddBody(p, category, tags, paused)
	if err != nil {
		return fmt.Errorf("download: qui: build request body: %w", err)
	}

	path := fmt.Sprintf("/api/instances/%d/torrents", d.instanceID)
	_, err = d.jc.Do(ctx, http.MethodPost, path, apphttp.RawBody{ContentType: contentType, Data: body}, nil)
	return err
}

// quiAddBody builds the multipart body for POST /api/instances/{id}/torrents: a
// `torrent` file part for fetched bytes, or a `urls` field for a magnet/http link
// the client fetches itself — plus category/tags/paused. Field names mirror
// qBittorrent's own torrents/add (per #242's verified API surface).
func quiAddBody(p Payload, category string, tags []string, paused bool) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if category != "" {
		if err := mw.WriteField("category", category); err != nil {
			return nil, "", fmt.Errorf("write category field: %w", err)
		}
	}
	if len(tags) > 0 {
		if err := mw.WriteField("tags", strings.Join(tags, ",")); err != nil {
			return nil, "", fmt.Errorf("write tags field: %w", err)
		}
	}
	if err := mw.WriteField("paused", strconv.FormatBool(paused)); err != nil {
		return nil, "", fmt.Errorf("write paused field: %w", err)
	}

	if len(p.Bytes) > 0 {
		fw, err := mw.CreateFormFile("torrent", cmp.Or(p.Name, "upload.torrent"))
		if err != nil {
			return nil, "", fmt.Errorf("create torrent file part: %w", err)
		}
		if _, err := fw.Write(p.Bytes); err != nil {
			return nil, "", fmt.Errorf("write torrent file part: %w", err)
		}
	} else if err := mw.WriteField("urls", p.URL); err != nil {
		return nil, "", fmt.Errorf("write urls field: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}
