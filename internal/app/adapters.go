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
	apphttp "github.com/autobrr/harbrr/internal/http"
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

// announcePushTimeoutMax is the flat hard cap on one fill's whole push fan-out, so a
// huge fill against slow targets can't hold a worker (and its queue slot) forever. The
// announce Service scales each CONNECTION's budget by its release count internally
// (connPushBudget) — this outer deadline only backstops the total.
const announcePushTimeoutMax = 10 * time.Minute

// maxConcurrentAnnouncePushes sizes the fixed worker pool that processes queued announce
// pushes, bounding how many run at once so a burst of RSS fills (or a slow/down announce
// target) cannot consume unbounded resources.
const maxConcurrentAnnouncePushes = 8

// announcePushQueueCapacity bounds how many fills can wait for a free worker before
// newAnnounceSink starts dropping (see announceQueueEnqueueGrace). Sized as a small multiple
// of the worker pool so a burst queues rather than instantly drops.
const announcePushQueueCapacity = maxConcurrentAnnouncePushes * 4

// announceQueueEnqueueGrace is how long a fill waits for a free queue slot before it's
// dropped. A slow target should cost latency, not delivery (#232) — but the sink runs on the
// caller's (RSS poll) goroutine, so the wait is bounded rather than indefinite.
const announceQueueEnqueueGrace = 2 * time.Second

// srcRelease is the minimal snapshot the announce sink lifts out of a cache write-back, so
// the async push never holds (or races on) the cached release slice.
type srcRelease struct {
	name, guid, link, magnet string
	size                     int64
}

// newAnnounceSink builds the cross-seed announce source: a registry.AnnounceSink that, on an
// RSS/empty-query cache fill, asynchronously pushes the new releases to every enabled
// announce target. The HTTP fan-out runs on a fixed worker pool (its own goroutines + a
// fresh, per-batch context sized off the release count), so a push never blocks or fails a
// search; only the cheap snapshot + enqueue runs on the caller's goroutine. A fill queues
// behind a slow pool rather than dropping immediately — it's dropped only if the queue
// itself stays full past announceQueueEnqueueGrace (#232).
func newAnnounceSink(svc *announce.Service, db dbinterface.Execer, keyring *secrets.Keyring, basePath, externalOrigin string, log zerolog.Logger) registry.AnnounceSink {
	instances := database.Instances{}
	queue := make(chan func(), announcePushQueueCapacity)
	for range maxConcurrentAnnouncePushes {
		//nolint:gosec // G118: intentionally detached — workers must outlive any single triggering request.
		go func() {
			for job := range queue {
				job()
			}
		}()
	}
	return func(_ context.Context, instanceID int64, fresh []*normalizer.Release) {
		snap := make([]srcRelease, 0, len(fresh))
		for _, r := range fresh {
			snap = append(snap, srcRelease{name: r.Title, guid: torznab.GUIDFor(r), link: r.Link, magnet: r.Magnet, size: r.Size})
		}
		job := func() {
			ctx, cancel := context.WithTimeout(context.Background(), announcePushTimeoutMax)
			defer cancel()
			inst, err := instances.GetByID(ctx, db, instanceID)
			if err != nil {
				log.Warn().Err(err).Int64("instance_id", instanceID).Msg("announce: resolve indexer slug failed")
				return
			}
			// Every announce target today (qui cross-seed, cross-seed v6) is
			// torrent-only — pushing usenet releases at them is pure waste and
			// burns the push worker pool (#231).
			if inst.Protocol != "torrent" {
				log.Debug().Str("indexer", inst.Slug).Str("protocol", inst.Protocol).
					Msg("announce: skipping push for non-torrent indexer")
				return
			}
			svc.Push(ctx, func(conn domain.AnnounceConnection) []announce.Release {
				return announceReleasesFor(conn, svc, keyring, basePath, externalOrigin, inst.Slug, snap, log)
			})
		}
		select {
		case queue <- job:
		case <-time.After(announceQueueEnqueueGrace):
			log.Warn().Int64("instance_id", instanceID).Int("releases", len(snap)).
				Msg("announce: push backpressure — too many in-flight pushes; dropping (next RSS poll re-derives)")
		}
	}
}

// announceOrigin picks the /dl base origin for an announce push: the operator-configured
// server.external_url when set (authoritative over the connection's own stored harbrr
// URL, cutting the drift the serverinfo staleness check otherwise works around), else
// the connection's HarbrrURL as before.
func announceOrigin(externalOrigin, harbrrURL string) string {
	if externalOrigin != "" {
		return externalOrigin
	}
	return strings.TrimRight(harbrrURL, "/")
}

// announceReleasesFor projects the source snapshot into per-connection announce.Release
// values: the DownloadURL is a magnet as-is (public, no secret) or a sealed /dl proxy URL
// built from the sink's origin + its minted key, so the passkey never leaves harbrr. The
// origin is the operator-configured server.external_url when set — authoritative over the
// connection's own stored harbrr URL, cutting the drift the serverinfo staleness check
// otherwise works around — else the per-connection HarbrrURL as before. A release with no
// acquirable link is dropped.
func announceReleasesFor(conn domain.AnnounceConnection, svc *announce.Service, keyring *secrets.Keyring, basePath, externalOrigin, slug string, snap []srcRelease, log zerolog.Logger) []announce.Release {
	harbrrKey, err := svc.HarbrrKey(conn)
	if err != nil {
		log.Warn().Int64("connection_id", conn.ID).Msg("announce: decrypt harbrr key failed")
		return nil
	}
	dlBase := torznabhttp.DLBaseURLForOrigin(announceOrigin(externalOrigin, conn.HarbrrURL), basePath, slug)
	out := make([]announce.Release, 0, len(snap))
	for _, s := range snap {
		dl := s.magnet
		if dl == "" && s.link != "" {
			sealed, serr := torznabhttp.SealedDLURL(keyring, slug, dlBase, harbrrKey, s.link)
			if serr != nil {
				// The error never carries the link, and the guid is scrubbed: for
				// passkey-in-GUID trackers (FileList-style) the guid IS the
				// credential-bearing download URL (#230).
				log.Warn().Int64("connection_id", conn.ID).Str("indexer", slug).
					Str("guid", apphttp.RedactURL(s.guid)).
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
