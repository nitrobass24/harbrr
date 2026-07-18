package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

const (
	// maxTorrentFetchBytes caps a blackhole-fetched .torrent. Real .torrent files
	// are KB-scale; this matches announce's own torrent-fetch cap.
	maxTorrentFetchBytes = 8 << 20 // 8 MiB
	// maxNZBFetchBytes caps a blackhole-fetched .nzb. NZBs for large releases
	// (4K remuxes, season packs) routinely exceed the torrent cap, so usenet gets
	// a separate, larger ceiling.
	maxNZBFetchBytes = 64 << 20 // 64 MiB

	// maxSanitizedNameLen bounds the release-name-derived filename so an
	// absurdly long name can't produce an unwieldy (or filesystem-rejected) path.
	maxSanitizedNameLen = 200
)

// errMagnetNotSaved is returned by Add when a magnet-only release arrives and
// the client's settings don't opt into writing .magnet files — no silent drop.
var errMagnetNotSaved = errors.New("download: blackhole: magnet URI requires saveMagnetFiles")

// blackholeDriver writes a resolved release as a complete file into a
// configured watch folder for a real client to pick up. It has no network
// endpoint of its own (host is hostNone) and no auth (secret is unused).
type blackholeDriver struct {
	settings domain.BlackholeSettings
	client   *http.Client
}

// newBlackhole builds the blackhole driver from a configured client row.
func newBlackhole(c domain.DownloadClient, _ string, client *http.Client) (Driver, error) {
	var settings domain.BlackholeSettings
	if c.Settings.Blackhole != nil {
		settings = *c.Settings.Blackhole
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &blackholeDriver{settings: settings, client: client}, nil
}

// Test proves each configured dir is writable by creating and deleting a
// probe file (existence alone doesn't prove writability).
func (d *blackholeDriver) Test(_ context.Context) error {
	for _, dir := range []string{d.settings.TorrentDir, d.settings.NZBDir} {
		if dir == "" {
			continue
		}
		if err := probeWritable(dir); err != nil {
			return err
		}
	}
	return nil
}

// probeWritable creates and removes a temp file in dir, naming only the dir on
// failure — nothing else about the environment is exposed.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".harbrr-probe-*")
	if err != nil {
		return fmt.Errorf("download: blackhole: %s is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("download: blackhole: %s is not writable: %w", dir, err)
	}
	return nil
}

// Add writes the payload into the protocol's configured watch folder: fetched
// bytes go straight to disk, a URL without bytes is fetched first (blackhole
// has no "hand the client a URL" fallback — a magnet URI is the sole
// exception, since it has no fetchable content). AddOptions is unused: a
// watch folder has no category/tags/paused concept of its own — whatever
// polls the folder applies its own categorization.
func (d *blackholeDriver) Add(ctx context.Context, p Payload, _ AddOptions) error {
	dir, ext, limit, err := dirForProtocol(d.settings, p.Protocol)
	if err != nil {
		return err
	}

	if p.Protocol == ProtocolTorrent && len(p.Bytes) == 0 && strings.HasPrefix(p.URL, "magnet:") {
		if !d.settings.SaveMagnetFiles {
			return errMagnetNotSaved
		}
		return writeAtomic(dir, sanitizeName(p.Name)+".magnet", []byte(p.URL+"\n"))
	}

	data := p.Bytes
	if len(data) == 0 {
		if p.URL == "" {
			return fmt.Errorf("download: blackhole: %s: empty payload (no bytes or URL)", p.Protocol)
		}
		if data, err = fetchBytes(ctx, d.client, p.URL, limit); err != nil {
			return err
		}
	}
	return writeAtomic(dir, sanitizeName(p.Name)+ext, data)
}

// dirForProtocol resolves a payload's protocol to its configured dir,
// filename extension, and fetch-size cap. An unset dir means the client isn't
// configured to handle that protocol.
func dirForProtocol(s domain.BlackholeSettings, proto Protocol) (dir, ext string, limit int64, err error) {
	switch proto {
	case ProtocolTorrent:
		if s.TorrentDir == "" {
			return "", "", 0, fmt.Errorf("download: blackhole: %w: torrent", ErrUnsupportedProtocol)
		}
		return s.TorrentDir, ".torrent", maxTorrentFetchBytes, nil
	case ProtocolUsenet:
		if s.NZBDir == "" {
			return "", "", 0, fmt.Errorf("download: blackhole: %w: usenet", ErrUnsupportedProtocol)
		}
		return s.NZBDir, ".nzb", maxNZBFetchBytes, nil
	default:
		return "", "", 0, fmt.Errorf("download: blackhole: %w: %s", ErrUnsupportedProtocol, proto)
	}
}

// fetchBytes GETs url and returns its body, capped at limit bytes. Any error
// is scrubbed of the URL — a sealed harbrr /dl link or an indexer's nzb URL
// can carry an apikey/token.
func fetchBytes(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download: blackhole: build fetch request: %w", apphttp.RedactURLError(err))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: blackhole: fetch %s: %w", apphttp.RedactURL(url), apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download: blackhole: fetch %s: status %d", apphttp.RedactURL(url), resp.StatusCode)
	}
	// Read one byte past the cap so an oversized body is rejected rather than
	// silently truncated (a partial torrent/nzb on disk would be garbage).
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download: blackhole: read body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download: blackhole: fetch %s: exceeds %d bytes", apphttp.RedactURL(url), limit)
	}
	return data, nil
}

// writeAtomic writes data into dir/filename via a temp file + rename, so a
// watching client never sees a partial file. os.CreateTemp already creates the
// file 0600; renaming over an existing file makes a re-grab idempotent.
func writeAtomic(dir, filename string, data []byte) error {
	f, err := os.CreateTemp(dir, ".harbrr-*")
	if err != nil {
		return fmt.Errorf("download: blackhole: create temp file in %s: %w", dir, err)
	}
	tmpName := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("download: blackhole: write %s: %w", filename, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("download: blackhole: close %s: %w", filename, err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, filename)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("download: blackhole: rename to %s: %w", filename, err)
	}
	return nil
}

// sanitizeName strips path separators and NUL from a release name so it can
// never escape dir when joined with an extension, and bounds its length.
func sanitizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(name))
	if name == "" {
		name = "release"
	}
	if len(name) > maxSanitizedNameLen {
		name = name[:maxSanitizedNameLen]
	}
	return name
}
