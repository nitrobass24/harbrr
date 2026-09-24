package http

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
)

// GetCapped GETs url with client and returns the whole body, capped at limit bytes.
// An oversized body is an ERROR, not a truncation: the two callers fetch a .torrent
// and an .nzb, and a partial one is corrupt rather than short.
//
// It deliberately never names url in an error. Both callers GET a credential-bearing
// link (harbrr's own /dl carries its apikey; an indexer's nzb link carries a token),
// so a transport *url.Error — whose Error() quotes the full URL — is stripped to its
// Op by ScrubURLError, and whether a redacted form of the URL belongs in the message
// is the caller's decision in its own wrap.
func GetCapped(ctx context.Context, client *stdhttp.Client, url string, limit int64) ([]byte, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", ScrubURLError(err))
	}
	// G704: url is a harbrr /dl link or an indexer download link resolved from a
	// search result — fetching it is the whole point, not attacker-steered SSRF.
	resp, err := client.Do(req)
	if err != nil {
		return nil, ScrubURLError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	// Read one byte past the cap so an oversized body is rejected rather than
	// silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("exceeds %d bytes", limit)
	}
	return data, nil
}
