package announce

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// maxTorrentBytes caps a fetched .torrent so a hostile/oversized /dl response can't exhaust
// memory (real .torrent files are KB-scale; this is generous).
const maxTorrentBytes = 8 << 20 // 8 MiB

// DefaultTargetFactory builds the production per-kind announce driver. client is shared by
// the HTTP calls; fetch fetches the .torrent for qui's apply step (nil falls back to an
// HTTP GET of the release's /dl URL); tags are applied to qui-injected torrents.
func DefaultTargetFactory(client *http.Client, fetch TorrentFetcher, tags []string) TargetFactory {
	if client == nil {
		client = defaultHTTPClient()
	}
	if fetch == nil {
		fetch = HTTPTorrentFetcher(client)
	}
	return func(conn domain.AnnounceConnection, toolKey string) (Target, error) {
		switch conn.Kind {
		case domain.AnnounceKindQui:
			return NewQui(conn.BaseURL, toolKey, client, fetch, tags), nil
		case domain.AnnounceKindCrossSeedV6:
			return NewCrossSeedV6(conn.BaseURL, toolKey, client), nil
		default:
			return nil, fmt.Errorf("%w: unknown kind %q", domain.ErrInvalid, conn.Kind)
		}
	}
}

// HTTPTorrentFetcher fetches the .torrent bytes by GETting harbrr's own /dl URL (which
// resolves the tracker link server-side and streams the torrent). The URL carries harbrr's
// apikey; it is never logged, and a transport error is scrubbed of the URL by the caller.
func HTTPTorrentFetcher(client *http.Client) TorrentFetcher {
	if client == nil {
		client = defaultHTTPClient()
	}
	return func(ctx context.Context, downloadURL string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build /dl request: %w", scrubURLError(err))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch /dl: %w", scrubURLError(err))
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch /dl: status %d", resp.StatusCode)
		}
		// Read one byte past the cap so an oversized body is rejected rather than silently
		// truncated (a partial torrent base64-posted to qui would be garbage).
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxTorrentBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read /dl body: %w", err)
		}
		if len(data) > maxTorrentBytes {
			return nil, fmt.Errorf("read /dl body: exceeds %d bytes", maxTorrentBytes)
		}
		return data, nil
	}
}
