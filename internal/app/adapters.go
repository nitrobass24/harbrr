package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/torznab"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// appSyncClient is the HTTP client app-sync drivers use to reach the *arr/qui apps.
// A bounded timeout keeps a hung app from stalling a sync. It is the default
// client for New; WithHTTPClient overrides it (test-widening seam).
func appSyncClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// registrySource adapts the indexer registry to appsync.IndexerSource: the configured
// instances and each one's advertised Newznab categories. Keeping it in the
// composition root keeps the appsync package free of an engine dependency.
type registrySource struct {
	reg *registry.Registry
}

func (s registrySource) List(ctx context.Context) ([]domain.IndexerInstance, error) {
	return s.reg.List(ctx) //nolint:wrapcheck // composition-root adapter; the service wraps.
}

// Categories returns the indexer's advertised categories. An indexer that fails to
// resolve yields no categories rather than failing the whole sync — the indexer is
// still pushed (the app falls back to all categories).
func (s registrySource) Categories(ctx context.Context, slug string) ([]appsync.Category, error) {
	idx, ok := s.reg.Indexer(ctx, slug)
	if !ok {
		return nil, nil
	}
	caps := idx.Capabilities()
	out := make([]appsync.Category, 0, len(caps.Categories))
	for _, c := range caps.Categories {
		out = append(out, appsync.Category{ID: c.ID, Name: c.Name})
	}
	return out, nil
}

// Capabilities returns the indexer's flat Torznab capability tokens (tv-search,
// movie-search-imdbid, ...) derived from its advertised search modes — what qui
// stores per indexer. An unresolvable indexer yields none rather than failing the
// sync.
func (s registrySource) Capabilities(ctx context.Context, slug string) ([]string, error) {
	idx, ok := s.reg.Indexer(ctx, slug)
	if !ok {
		return nil, nil
	}
	return torznab.CapabilityTokens(idx.Capabilities()), nil
}

// apiKeyValidator wires the Torznab apikey check to the auth service so any minted
// key (stored only as a hash) authorizes the feed.
func apiKeyValidator(authSvc *auth.Service) func(string) bool {
	return func(key string) bool {
		_, err := authSvc.ValidateAPIKey(context.Background(), key)
		return err == nil
	}
}

// announcePushTimeout bounds one detached announce-push fan-out.
const announcePushTimeout = 60 * time.Second

// maxConcurrentAnnouncePushes bounds in-flight detached pushes so a burst of RSS fills (or
// a slow/down announce target holding goroutines for the full timeout) cannot pile up
// without limit and starve request handling. Excess fills are dropped with a log rather
// than queued — the next RSS poll re-derives the same "what's new" set.
const maxConcurrentAnnouncePushes = 8

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
	sem := make(chan struct{}, maxConcurrentAnnouncePushes)
	return func(_ context.Context, instanceID int64, fresh []*normalizer.Release) {
		snap := make([]srcRelease, 0, len(fresh))
		for _, r := range fresh {
			snap = append(snap, srcRelease{name: r.Title, guid: torznab.GUIDFor(r), link: r.Link, magnet: r.Magnet, size: r.Size})
		}
		select {
		case sem <- struct{}{}:
		default:
			log.Warn().Int64("instance_id", instanceID).Int("releases", len(snap)).
				Msg("announce: push backpressure — too many in-flight pushes; dropping (next RSS poll re-derives)")
			return
		}
		//nolint:gosec // G118: intentionally detached — the announce push must outlive the triggering search request.
		go func() {
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), announcePushTimeout)
			defer cancel()
			inst, err := instances.GetByID(ctx, db, instanceID)
			if err != nil {
				log.Warn().Err(err).Int64("instance_id", instanceID).Msg("announce: resolve indexer slug failed")
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
	dlBase := torznabhttp.DLBaseURLForOrigin(strings.TrimRight(conn.HarbrrURL, "/"), basePath, slug)
	out := make([]announce.Release, 0, len(snap))
	for _, s := range snap {
		dl := s.magnet
		if dl == "" && s.link != "" {
			sealed, serr := torznabhttp.SealedDLURL(keyring, slug, dlBase, harbrrKey, s.link)
			if serr != nil {
				// The error never carries the link; log non-secret context so a dropped
				// release is debuggable rather than vanishing silently.
				log.Warn().Int64("connection_id", conn.ID).Str("indexer", slug).Str("guid", s.guid).
					Msg("announce: seal /dl link failed; skipping release")
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
