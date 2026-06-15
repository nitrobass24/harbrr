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
	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// indexerAdapter presents a built Cardigann engine as a torznab.Indexer, so the
// Torznab handler depends only on the interface, never the engine. It is the unit
// the registry caches per slug. It also records per-indexer health events: a
// classified Search failure appends one event (append-only) so the management
// status endpoint can surface why an indexer is unhealthy.
type indexerAdapter struct {
	info       torznab.IndexerInfo
	engine     *cardigann.Engine
	instanceID int64
	db         dbinterface.Execer
	health     database.Health
	clock      func() time.Time
	log        zerolog.Logger
}

// Compile-time proof the adapter satisfies the handler's contract.
var _ torznab.Indexer = (*indexerAdapter)(nil)

// Info returns the indexer identity (carries no secrets).
func (a *indexerAdapter) Info() torznab.IndexerInfo { return a.info }

// Capabilities returns the engine's capabilities document.
func (a *indexerAdapter) Capabilities() *mapper.Capabilities { return a.engine.Capabilities() }

// Search runs the engine's online search. A classified failure (auth/anti-bot/
// rate-limited/parse) is recorded as a health event before the error is wrapped
// with the indexer id (not a secret) and returned; the caller redacts it.
func (a *indexerAdapter) Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error) {
	releases, err := a.engine.Search(ctx, query)
	if err != nil {
		a.recordHealth(ctx, err)
		return nil, fmt.Errorf("registry: search %q: %w", a.info.ID, err)
	}
	return releases, nil
}

// NeedsResolver reports whether the definition declares a download block.
func (a *indexerAdapter) NeedsResolver() bool { return a.engine.NeedsResolver() }

// ResolveDownload resolves a release link to the real torrent URL. The error is
// wrapped with the indexer id (not a secret); the caller redacts it.
func (a *indexerAdapter) ResolveDownload(ctx context.Context, link string) (string, error) {
	resolved, err := a.engine.ResolveDownload(ctx, link)
	if err != nil {
		return "", fmt.Errorf("registry: resolve download %q: %w", a.info.ID, err)
	}
	return resolved, nil
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
		a.log.Warn().Str("indexer", a.info.ID).Str("error", apphttp.RedactURL(rerr.Error())).
			Msg("registry: record health event failed")
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
