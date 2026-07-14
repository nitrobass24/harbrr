package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// indexerAdapter presents a built indexer (the Cardigann engine OR a native family
// driver) as a torznabhttp.Indexer, so the Torznab handler depends only on the
// interface, never the concrete engine. It is the unit the registry caches per
// slug. It also records per-indexer health events: a classified Search failure
// appends one event (append-only) so the management status endpoint can surface why
// an indexer is unhealthy.
type indexerAdapter struct {
	info       torznabhttp.IndexerInfo
	inner      native.Driver
	instanceID int64
	// cfg is the decrypted per-instance settings map. Search's cache-aside stage reads
	// its "cache_ttl" override; it carries secrets, so it is never logged.
	cfg map[string]string
	// cache is the registry-wide search cache, wired at build time when caching is
	// configured (nil ⇒ caching not configured, so Search runs live). builtEpoch is the
	// instance's invalidation generation snapshotted at that same build time (see
	// Registry.build); storeBestEffort drops any write-back from a superseded generation
	// (U8R-F4). Snapshotting at build — not per fetch — also catches a purge that lands
	// between the resolve and a later SWR trigger.
	cache      *SearchCache
	builtEpoch uint64
	// freeleechOnly is the instance's stored `freeleech` setting. The engine is built
	// with that key cleared (so it always fetches the full catalog); the value is
	// carried here only to drive the serve-time freeleech view in Search.
	freeleechOnly bool
	db            dbinterface.Execer
	health        database.Health
	// healthSink, when non-nil, is notified best-effort after a health event is
	// recorded so a subsystem (notify) can fan it out to configured targets. It must
	// not block or fail back into Search.
	healthSink HealthSink
	// stats records the durable per-indexer query/grab/latency counters. Increments are
	// in-memory atomics (no hot-path DB write); the registry flushes them periodically.
	stats *IndexerStats
	clock func() time.Time
	log   zerolog.Logger
}

// Compile-time proof the adapter satisfies the handler's contract, including
// SupportsOffsetPaging — now part of torznabhttp.Indexer proper, so this single
// assertion replaces the runtime capability re-forwarding the old freeleech/cache
// decorators had to hand-write on every layer.
var _ torznabhttp.Indexer = (*indexerAdapter)(nil)

// Info returns the indexer identity (carries no secrets).
func (a *indexerAdapter) Info() torznabhttp.IndexerInfo { return a.info }

// Capabilities returns the built indexer's capabilities document.
func (a *indexerAdapter) Capabilities() *mapper.Capabilities { return a.inner.Capabilities() }

// Search is the served entry point. It sequences two stages in a fixed order: the
// cache-aside read over the FULL catalog, then the freeleech serve-time view applied to
// the cache's OUTPUT. Keeping freeleech OUTSIDE the cache is what lets one cached
// full-catalog entry serve both the honor feed (freeleech-only, for the *arrs) and the
// bypass feed (full catalog, for qui/cross-seed) from a SINGLE tracker fetch — so a later
// bypass poll never re-hits the tracker just because an *arr polled FL-only first.
func (a *indexerAdapter) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	// (1) Cache-aside over the full catalog. The two-level enabled distinction lives
	// here: cache nil (never configured) OR the runtime toggle off ⇒ run liveSearch
	// directly; otherwise the cache drives liveSearch on a miss so the tracker is hit
	// exactly once. SupportsOffsetPaging is the SAME signal the handler reads, so a paging
	// driver keys per-page in the cache and is not re-offset downstream.
	var (
		releases []*normalizer.Release
		err      error
	)
	if a.cache != nil && a.cache.tuning.Load().enabled {
		releases, err = a.cache.search(ctx, a.instanceID, a.cfg, a.builtEpoch, a.liveSearch, a.SupportsOffsetPaging(), q)
	} else {
		releases, err = a.liveSearch(ctx, q)
	}
	if err != nil {
		return nil, err
	}

	// (2) Freeleech serve-time view, over the stored full catalog. The freeleech signal
	// is downloadVolumeFactor == 0 — the per-row marker every freeleech def stamps
	// independent of the setting. The bypass feed sets q.FreeleechBypass to skip it and
	// reuse the same cached entry.
	//
	// Paging note: this filter runs INSIDE the Search the handler's pager measures, so on
	// a deep-paging driver the honor feed's has-more floor is computed on the post-filter
	// page and can stop early (the documented pagination-dilution divergence). This is
	// unreachable for the shipped paging drivers — only usenet drivers (newznab, nzbindex)
	// forward offset upstream, and usenet has no freeleech setting, so freeleechOnly is
	// always false there.
	if a.freeleechOnly && !q.FreeleechBypass {
		releases = filterFreeleechOnly(releases)
	}
	return releases, nil
}

// liveSearch is the live seam the cache drives on a miss or a refresh (and that Search
// calls directly when caching is off): it runs the engine's online search and returns the
// FULL catalog. A classified failure (auth/anti-bot/rate-limited/parse) is recorded as a
// health event before the error is wrapped with the indexer id (not a secret) and
// returned; the caller redacts it.
func (a *indexerAdapter) liveSearch(ctx context.Context, query search.Query) ([]*normalizer.Release, error) {
	// Count every search that reaches the tracker (liveSearch is bypassed on a cache hit)
	// and sample its latency around the inner call — a failed search is still a query
	// attempt with a real latency sample.
	start := a.clock()
	releases, err := a.inner.Search(ctx, query)
	a.stats.RecordQuery(a.instanceID, a.clock().Sub(start))
	if err != nil {
		a.recordHealth(ctx, err)
		return nil, fmt.Errorf("registry: search %q: %w", a.info.ID, err)
	}
	return releases, nil
}

// NeedsResolver reports whether the definition declares a download block.
func (a *indexerAdapter) NeedsResolver() bool { return a.inner.NeedsResolver() }

// DownloadNeedsAuth reports whether the download authenticates out-of-band (session
// cookie / request header), so its link must be routed through /dl rather than served
// bare.
func (a *indexerAdapter) DownloadNeedsAuth() bool { return a.inner.DownloadNeedsAuth() }

// SupportsOffsetPaging delegates to the wrapped driver's SupportsOffsetPaging, part of
// the native.Driver contract: false for every Cardigann def and every native driver
// except the newznab and nzbindex usenet pair. When true, the handler forwards
// offset/limit upstream and does not re-slice the returned page. The adapter promotes
// the signal so the cache layer (which keys per-page for paging drivers) and the
// handler read the SAME capability.
func (a *indexerAdapter) SupportsOffsetPaging() bool {
	return a.inner.SupportsOffsetPaging()
}

// Grab performs the grab-time download for a release link (resolve + fetch the
// torrent through the session). The error is wrapped with the indexer id (not a
// secret); the caller redacts it. This is the /dl proxy's seam; feed serialization
// only tokenizes the link, so no resolution runs per served release.
func (a *indexerAdapter) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	result, err := a.inner.Grab(ctx, link)
	if err != nil {
		// Classify grab-time failures too: a 429/503 rate-limit, a first-op login/
		// anti-bot failure on a fresh engine, and the native drivers' auth sentinels
		// all reach here. classifyHealth no-ops on an unclassified error, so an
		// ordinary grab failure records nothing. Mirrors Search.
		a.recordHealth(ctx, err)
		return nil, fmt.Errorf("registry: grab %q: %w", a.info.ID, err)
	}
	// Count success only — a failed grab produced no download.
	a.stats.RecordGrab(a.instanceID)
	return result, nil
}

// recordHealth classifies err and, when it is one of the four health kinds,
// appends a health event with a credential-scrubbed detail. It is best-effort:
// a failed write is logged (redacted) and never masks the original search error.
func (a *indexerAdapter) recordHealth(ctx context.Context, err error) {
	kind, ok := classifyHealth(err)
	if !ok {
		return
	}
	ev := domain.IndexerHealthEvent{
		InstanceID: a.instanceID,
		Kind:       kind,
		Detail:     apphttp.RedactError(err),
		OccurredAt: a.clock(),
	}
	if rerr := a.health.Record(ctx, a.db, ev); rerr != nil {
		a.log.Warn().Str("indexer", a.info.ID).Str("error", apphttp.RedactError(rerr)).
			Msg("registry: record health event failed")
	}
	// Notify the sink after recording, best-effort: it owns its own async dispatch and
	// must never block or error back into the search path. The detail is already
	// scrubbed (RedactError above).
	if a.healthSink != nil {
		a.healthSink.OnHealthEvent(ctx, a.info.ID, ev.Kind, ev.Detail)
	}
}

// classifyHealth maps an engine error to a health-event kind. Returns ok=false
// for errors outside the four categories (no event recorded).
func classifyHealth(err error) (string, bool) {
	switch {
	case errors.Is(err, login.ErrLoginFailed):
		return domain.HealthAuthFailure, true
	case errors.Is(err, login.ErrSolverRequired):
		return domain.HealthAntiBot, true
	case errors.Is(err, search.ErrRateLimited):
		return domain.HealthRateLimited, true
	case errors.Is(err, search.ErrParseError):
		return domain.HealthParseError, true
	default:
		return "", false
	}
}

// filterFreeleechOnly returns a NEW slice holding only freeleech releases
// (DownloadVolumeFactor == 0). It allocates fresh so the cached slice — shared with the
// bypass feed and the announce-source tap — is never mutated. Partial-leech releases
// (factor 0.5/0.75) are not freeleech and are excluded, matching Jackett's freeleech
// selector, which keys on the 100%-free marker.
func filterFreeleechOnly(releases []*normalizer.Release) []*normalizer.Release {
	out := make([]*normalizer.Release, 0, len(releases))
	for _, r := range releases {
		if r != nil && r.DownloadVolumeFactor == 0 {
			out = append(out, r)
		}
	}
	return out
}
