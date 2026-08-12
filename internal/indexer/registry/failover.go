package registry

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Reserved instance settings backing the base-URL failover (autobrr/harbrr#375).
// Both are ordinary non-secret settings, so an operator reads them in GET
// /api/indexers/{slug} and writes them with the same PATCH as any other setting —
// which is also how a promotion is reverted (PATCH settings.failover_base_url = "").
const (
	// failoverBaseURLSetting holds a base URL the failover promoted. It is
	// deliberately NOT the instance's BaseURL column: that column is what the
	// OPERATOR configured (#401's picker writes it), and overwriting it would destroy
	// the "configured A, currently using B, revert" distinction this feature owes the
	// operator.
	failoverBaseURLSetting = "failover_base_url"
	// failoverDisabledSetting is the operator pin: any truthy value opts this indexer
	// out of failover entirely, whatever the failure shape. Pinned is pinned. It is
	// spelled "disabled" rather than "pin" because "pin" is one of the loader's
	// secret name tokens, so a setting carrying it would be encrypted at rest and read
	// back as <redacted> — the operator could never see whether the pin was on.
	//
	// Note the #401 contract: SELECTING a base URL in the picker is not pinning. Only
	// this setting disables failover.
	failoverDisabledSetting = "failover_disabled"
)

const (
	// failoverAfter is how long an instance must have been failing without a single
	// success before a rotation is considered — "repeated, not first". The failure
	// streak is the circuit breaker's own InitialFailure, which a success clears, so
	// this is genuinely continuous breakage and not a blip. Ten minutes is ~10 failed
	// attempts at the transport rung's 60-second window, and still rotates on the
	// second failed poll of a 15-minute RSS cadence.
	failoverAfter = 10 * time.Minute
	// failoverStreak is how many of the most recent health events must ALL be
	// transport failures for a rotation to qualify. This is the credential guard: an
	// auth failure anywhere in the recent history means the host is answering and
	// something we SENT is wrong, and rotating there would present bad credentials to
	// every mirror in turn. Two is the smallest number that means "repeated".
	failoverStreak = 2
	// failoverRetry is the backoff between probe cycles for one instance: walk the
	// candidate list once, then stay quiet for half an hour rather than re-walking it
	// on every query when every link is dead.
	failoverRetry = 30 * time.Minute
	// failoverProbeTimeout bounds ONE whole probe cycle (every candidate together),
	// because the cycle runs inline on the search that failed.
	failoverProbeTimeout = 30 * time.Second
)

// failoverGate admits one probe cycle per instance per failoverRetry. Stamping on
// ADMISSION rather than on completion also makes it the single-flight guard: two
// concurrent failing searches on the same indexer cannot both start a cycle.
//
// ponytail: no eviction on instance delete. An entry is one int64 + one time.Time and
// the only consequence of a stale one is that an instance re-added onto a recycled row
// id waits out the backoff once. Add a forget() to the instanceForgetter seam if that ever
// matters.
type failoverGate struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

// allow reports whether instance id may start a probe cycle now, stamping it when it may.
func (g *failoverGate) allow(id int64, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if last, ok := g.last[id]; ok && now.Sub(last) < failoverRetry {
		return false
	}
	if g.last == nil {
		g.last = make(map[int64]time.Time)
	}
	g.last[id] = now
	return true
}

// maybeFailover rotates this instance onto another of its definition's known links
// when the evidence says the configured HOST is gone. Called from recordHealth with
// the circuit state that failure just wrote. Best-effort throughout: it never masks
// the search/grab error that triggered it.
func (a *indexerAdapter) maybeFailover(ctx context.Context, kind string, state database.CircuitState) {
	if a.failover == nil || !a.failoverQualifies(ctx, kind, state) {
		return
	}
	promoted, err := a.failover(ctx)
	if err != nil {
		a.log.Warn().Str("indexer", a.info.ID).Str("error", apphttp.RedactError(err)).
			Msg("registry: base URL failover failed")
		return
	}
	if promoted == "" {
		return
	}
	a.recordPromotion(ctx, promoted)
}

// failoverQualifies is the whole trigger condition, and every clause is a refusal the
// issue asks for by name:
//
//  1. ONLY a transport failure — DNS failure, connection refused, TLS failure, a
//     dropped connection, a gateway outage. An auth failure or a rate limit means the
//     host is fine and something else is wrong; anti-bot means it is answering with a
//     challenge; a parse error means it answered with something. Rotating on any of
//     those multiplies the problem across every mirror.
//  2. The operator pin wins over everything.
//  3. Repeated, not first: an unbroken failure streak at least failoverAfter long.
//  4. Nothing credential-shaped in the recent history (see failoverStreak).
func (a *indexerAdapter) failoverQualifies(ctx context.Context, kind string, state database.CircuitState) bool {
	if kind != domain.HealthTransport {
		return false
	}
	if settingEnabled(a.cfg[failoverDisabledSetting]) {
		return false
	}
	if state.InitialFailure.IsZero() || a.clock().Sub(state.InitialFailure) < failoverAfter {
		return false
	}
	return a.transportOnlyStreak(ctx)
}

// transportOnlyStreak reports whether the newest failoverStreak health events are all
// transport failures. A read error answers false: not knowing is not permission.
func (a *indexerAdapter) transportOnlyStreak(ctx context.Context) bool {
	events, err := a.health.Recent(ctx, a.db, a.instanceID, failoverStreak)
	if err != nil {
		a.log.Warn().Str("indexer", a.info.ID).Str("error", apphttp.RedactError(err)).
			Msg("registry: read health events failed")
		return false
	}
	if len(events) < failoverStreak {
		return false
	}
	return !slices.ContainsFunc(events, func(e domain.IndexerHealthEvent) bool {
		return e.Kind != domain.HealthTransport
	})
}

// recordPromotion makes a rotation visible, audible, and usable: an entry in the
// indexer's health timeline, a notification through the same debounced sink health
// failures use (failover is never silent — operators who maintain definitions want to
// LEARN that a site moved), and the circuit recovery that lets the promoted host be
// dispatched to immediately instead of staying gated behind the disable window the
// failure just set.
//
// The recovery watermark is advanced AFTER the event so the promotion sits inside it:
// otherwise the newest event on the indexer would be a promotion, and the status
// derivation — which reads the newest event as the newest FAILURE — would report a
// just-repaired indexer as failing for the whole failing window.
func (a *indexerAdapter) recordPromotion(ctx context.Context, host string) {
	ev := domain.IndexerHealthEvent{
		InstanceID: a.instanceID,
		Kind:       domain.HealthBaseURLPromoted,
		// A definition link is public, not a credential; nothing here needs redacting.
		Detail:     fmt.Sprintf("Base URL failed over to %s, a host this definition already lists — the configured one stopped answering.", host),
		OccurredAt: a.clock(),
	}
	if err := a.health.Record(ctx, a.db, ev); err != nil {
		a.log.Warn().Str("indexer", a.info.ID).Str("error", apphttp.RedactError(err)).
			Msg("registry: record base URL promotion failed")
	}
	if err := a.health.RecordRecovery(ctx, a.db, a.instanceID, a.clock()); err != nil {
		a.log.Warn().Str("indexer", a.info.ID).Str("error", apphttp.RedactError(err)).
			Msg("registry: record base URL promotion recovery failed")
	}
	a.recordCircuitSuccess(ctx)
	if a.healthSink != nil {
		a.healthSink.OnHealthEvent(ctx, a.info.ID, ev.Kind, ev.Detail)
	}
}

// failover probes the definition's OTHER known links and promotes the first that both
// authenticates and answers a search. It returns the promoted host, or "" when nothing
// was promoted: a single-link definition, an instance still inside its backoff, or a
// cycle where every candidate failed.
//
// ponytail: the cycle runs inline on the request whose failure triggered it, so that
// request can take up to failoverProbeTimeout longer. It is bounded to one cycle per
// failoverRetry, and only ever runs on an indexer that has been failing for ten
// minutes — whose caller is getting an error either way. Move it to a background
// sweeper if that latency ever shows up somewhere it matters.
func (r *Resolver) failover(ctx context.Context, inst domain.IndexerInstance, def *loader.Definition, current string) (string, error) {
	candidates := failoverCandidates(def, current)
	if len(candidates) == 0 || !r.failoverGate.allow(inst.ID, r.clock()) {
		return "", nil
	}
	// Detached from the caller: the client may hang up the moment the failing search
	// returns its error, and a promotion half-written is worse than none. Bounded
	// because it is still on the request path.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failoverProbeTimeout)
	defer cancel()
	for _, host := range candidates {
		if !r.probe(ctx, inst.Slug, host) {
			continue
		}
		if err := r.promote(ctx, inst, def, host); err != nil {
			return "", err
		}
		return host, nil
	}
	return "", nil
}

// probe builds a throwaway driver pinned to host and asks it to do the two things a
// promotion requires. Both, not either:
//   - the definition's own test/login path, so a candidate that answers HTTP but fails
//     login is a FAILED candidate and the walk continues;
//   - an actual search, because a definition with no login block passes the test path
//     without making a single request, and "passed" would then mean nothing at all
//     about whether the candidate host is alive.
//
// It drives the built driver directly rather than the adapter, so a probe records no
// health event, escalates no circuit, spends no request budget, and cannot recurse
// into another failover.
func (r *Resolver) probe(ctx context.Context, slug, host string) bool {
	a, err := r.buildAdapterAt(ctx, slug, host)
	if err != nil {
		r.log.Warn().Str("indexer", slug).Str("error", apphttp.RedactError(err)).
			Msg("registry: build failover candidate failed")
		return false
	}
	if err := a.inner.Test(ctx); err != nil {
		r.log.Info().Str("indexer", slug).Str("candidate", host).Str("error", apphttp.RedactError(err)).
			Msg("registry: failover candidate did not authenticate")
		return false
	}
	if _, err := a.inner.Search(ctx, search.Query{}); err != nil {
		r.log.Info().Str("indexer", slug).Str("candidate", host).Str("error", apphttp.RedactError(err)).
			Msg("registry: failover candidate did not answer a search")
		return false
	}
	return true
}

// promote durably records the new host and drops everything derived from the old one:
// the cached engine (so the next resolve talks to the promoted host) and the cached
// results (whose download links point at a host that just stopped answering).
func (r *Resolver) promote(ctx context.Context, inst domain.IndexerInstance, def *loader.Definition, host string) error {
	if err := r.persistSetting(ctx, inst, def, failoverBaseURLSetting, host); err != nil {
		return err
	}
	r.invalidate(inst.Slug)
	r.invalidateSearchCache(ctx, inst.ID)
	r.log.Warn().Str("indexer", inst.Slug).Str("base_url", host).
		Msg("registry: promoted a failover base URL")
	return nil
}

// failoverCandidates are the definition's links minus the host already in use — the
// ONLY hosts a probe may ever authenticate against. A candidate is never taken from a
// redirect, an error body, or anywhere else: if it is not in the definition (or typed
// by the operator, which is the configured override, not a candidate), harbrr does not
// send credentials to it.
func failoverCandidates(def *loader.Definition, current string) []string {
	out := make([]string, 0, len(def.Links))
	for _, link := range def.Links {
		if link == "" || sameHostURL(link, current) {
			continue
		}
		out = append(out, link)
	}
	return out
}

// linked reports whether host is one of the definition's links. Read at build time so
// a definition update that drops a dead mirror also un-promotes an instance sitting on
// it, instead of pinning that instance to a host the definition no longer vouches for.
func linked(def *loader.Definition, host string) bool {
	return slices.ContainsFunc(def.Links, func(link string) bool { return sameHostURL(link, host) })
}

// sameHostURL compares two base URLs ignoring a trailing slash and letter case, so a
// stored "https://Example.org/" still matches a definition link of "https://example.org"
// (the same normalisation the base-URL picker applies, #401).
func sameHostURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}
