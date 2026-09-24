package announce

import (
	"context"
	"fmt"
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// maxTorrentBytes caps a fetched .torrent so a hostile/oversized /dl response can't exhaust
// memory (real .torrent files are KB-scale; this is generous).
const maxTorrentBytes = 8 << 20 // 8 MiB

// DefaultTargetFactory builds the production per-kind announce driver. client (required)
// is shared by the HTTP calls, including the HTTP GET of the release's /dl URL that
// fetches the .torrent for qui's apply step. Injected torrents carry no tags.
func DefaultTargetFactory(client *http.Client) TargetFactory {
	fetch := HTTPTorrentFetcher(client)
	return func(conn domain.AnnounceConnection, toolKey string) (Target, error) {
		switch conn.Kind {
		case domain.AnnounceKindQui:
			return NewQui(conn.BaseURL, toolKey, client, fetch, nil), nil
		case domain.AnnounceKindCrossSeedV6:
			return NewCrossSeedV6(conn.BaseURL, toolKey, client), nil
		default:
			return nil, fmt.Errorf("%w: unknown kind %q", domain.ErrInvalid, conn.Kind)
		}
	}
}

// HTTPTorrentFetcher fetches the .torrent bytes by GETting harbrr's own /dl URL (which
// resolves the tracker link server-side and streams the torrent). The URL carries harbrr's
// apikey; it is never logged, and GetCapped scrubs it out of a transport error.
func HTTPTorrentFetcher(client *http.Client) TorrentFetcher {
	return func(ctx context.Context, downloadURL string) ([]byte, error) {
		data, err := apphttp.GetCapped(ctx, client, downloadURL, maxTorrentBytes)
		if err != nil {
			return nil, fmt.Errorf("fetch /dl: %w", err)
		}
		return data, nil
	}
}
