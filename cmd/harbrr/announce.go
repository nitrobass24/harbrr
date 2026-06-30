package main

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	tzn "github.com/autobrr/harbrr/internal/torznab"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// announcePushTimeout bounds one detached announce-push fan-out.
const announcePushTimeout = 60 * time.Second

// srcRelease is the minimal snapshot the announce sink lifts out of a cache write-back, so
// the async push never holds (or races on) the cached release slice.
type srcRelease struct {
	name, guid, link, magnet string
	size                     int64
}

// newAnnounceSink builds the cross-seed announce source: a registry.AnnounceSink that, on an
// RSS/empty-query cache fill, asynchronously pushes the new releases to every enabled
// announce target. The HTTP fan-out is detached (its own goroutine + a fresh, bounded
// context), so a push never blocks or fails a search; only the cheap snapshot loop runs on
// the caller's goroutine.
func newAnnounceSink(svc *announce.Service, db dbinterface.Execer, keyring *secrets.Keyring, basePath string, log zerolog.Logger) registry.AnnounceSink {
	instances := database.Instances{}
	return func(_ context.Context, instanceID int64, fresh []*normalizer.Release) {
		snap := make([]srcRelease, 0, len(fresh))
		for _, r := range fresh {
			snap = append(snap, srcRelease{name: r.Title, guid: tzn.GUIDFor(r), link: r.Link, magnet: r.Magnet, size: r.Size})
		}
		//nolint:gosec // G118: intentionally detached — the announce push must outlive the triggering search request.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), announcePushTimeout)
			defer cancel()
			inst, err := instances.GetByID(ctx, db, instanceID)
			if err != nil {
				log.Warn().Int64("instance_id", instanceID).Msg("announce: resolve indexer slug failed")
				return
			}
			svc.Push(ctx, func(conn domain.AnnounceConnection) []announce.Release {
				return announceReleasesFor(conn, svc, keyring, basePath, inst.Slug, snap, log)
			})
		}()
	}
}

// announceReleasesFor projects the source snapshot into per-connection announce.Release
// values: the DownloadURL is a magnet as-is (public, no secret) or a sealed /dl proxy URL
// built from the connection's harbrr URL + its minted key, so the passkey never leaves
// harbrr. A release with no acquirable link is dropped.
func announceReleasesFor(conn domain.AnnounceConnection, svc *announce.Service, keyring *secrets.Keyring, basePath, slug string, snap []srcRelease, log zerolog.Logger) []announce.Release {
	harbrrKey, err := svc.HarbrrKey(conn)
	if err != nil {
		log.Warn().Int64("connection_id", conn.ID).Msg("announce: decrypt harbrr key failed")
		return nil
	}
	dlBase := strings.TrimRight(conn.HarbrrURL, "/") + basePath + "/api/v2.0/indexers/" + url.PathEscape(slug) + "/dl"
	out := make([]announce.Release, 0, len(snap))
	for _, s := range snap {
		dl := s.magnet
		if dl == "" && s.link != "" {
			sealed, serr := torznabhttp.SealedDLURL(keyring, slug, dlBase, harbrrKey, s.link)
			if serr != nil {
				continue
			}
			dl = sealed
		}
		if dl == "" {
			continue
		}
		out = append(out, announce.Release{
			Name: s.name, Size: s.size, Indexer: slug, GUID: s.guid, Tracker: slug, DownloadURL: dl,
		})
	}
	return out
}
