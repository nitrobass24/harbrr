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
	// cfg is the decrypted per-instance settings map. The search-cache decorator
	// reads its "cache_ttl" override; it carries secrets, so it is never logged.
	cfg map[string]string
	// freeleechOnly is the instance's stored `freeleech` setting. The engine is built
	// with that key cleared (so it always fetches the full catalog); the value is
	// carried here only to drive the serve-time freeleechIndexer decorator.
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

// Compile-time proof the adapter satisfies the handler's contract.
var _ torznabhttp.Indexer = (*indexerAdapter)(nil)

// Info returns the indexer identity (carries no secrets).
func (a *indexerAdapter) Info() torznabhttp.IndexerInfo { return a.info }

// Capabilities returns the built indexer's capabilities document.
func (a *indexerAdapter) Capabilities() *mapper.Capabilities { return a.inner.Capabilities() }

// Search runs the engine's online search. A classified failure (auth/anti-bot/
// rate-limited/parse) is recorded as a health event before the error is wrapped
// with the indexer id (not a secret) and returned; the caller redacts it.
func (a *indexerAdapter) Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error) {
	// Count every search that reaches the tracker (this adapter is bypassed on a cache
	// hit) and sample its latency around the inner call — a failed search is still a
	// query attempt with a real latency sample.
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

// SupportsOffsetPaging delegates to the wrapped driver's optional OffsetPager capability,
// reporting false for any driver that doesn't implement it (every Cardigann def and every
// native driver except the newznab and nzbindex usenet pair). When true, the handler
// forwards offset/limit upstream and does not re-slice the returned page. The adapter
// promotes the signal so the cache layer (which keys per-page for paging drivers) and the
// handler read the SAME capability.
func (a *indexerAdapter) SupportsOffsetPaging() bool {
	if p, ok := a.inner.(native.OffsetPager); ok {
		return p.SupportsOffsetPaging()
	}
	return false
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
