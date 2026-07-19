package download

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	host       string
	apiKey     string
	instanceID int
	category   string
	tags       []string
	paused     bool
	client     *http.Client
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
		host:       strings.TrimRight(c.Host, "/"),
		apiKey:     secret,
		instanceID: settings.InstanceID,
		category:   settings.Category,
		tags:       settings.Tags,
		paused:     settings.StartPaused,
		client:     client,
	}, nil
}

// quiInstance is the subset of qui's GET /api/instances response Test reads.
type quiInstance struct {
	ID int `json:"id"`
}

// Test confirms the API key is valid and the configured instance id exists.
func (d *quiDriver) Test(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.host+"/api/instances", nil)
	if err != nil {
		return fmt.Errorf("download: qui: build request: %w", apphttp.RedactURLError(err))
	}
	req.Header.Set("X-API-Key", d.apiKey)

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("download: qui: %w", apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: qui: unexpected status %d", resp.StatusCode)
	}

	var instances []quiInstance
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return fmt.Errorf("download: qui: decode instances: %w", err)
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

	url := fmt.Sprintf("%s/api/instances/%d/torrents", d.host, d.instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("download: qui: build request: %w", apphttp.RedactURLError(err))
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-API-Key", d.apiKey)

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("download: qui: %w", apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download: qui: add torrent: unexpected status %d: %s", resp.StatusCode, apphttp.RedactError(errors.New(string(msg))))
	}
	return nil
}

// quiAddBody builds the multipart body for POST /api/instances/{id}/torrents: a
// `torrent` file part for fetched bytes, or a `urls` field for a magnet/http link
// the client fetches itself — plus category/tags/paused. Field names mirror
// qBittorrent's own torrents/add (per #242's verified API surface).
func quiAddBody(p Payload, category string, tags []string, paused bool) (*bytes.Buffer, string, error) {
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
	return &buf, mw.FormDataContentType(), nil
}
