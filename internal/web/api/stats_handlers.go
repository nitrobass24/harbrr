package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// indexerFailureCounts is the failure tally by health kind in the stats response.
type indexerFailureCounts struct {
	AuthFailure int64 `json:"authFailure"`
	RateLimited int64 `json:"rateLimited"`
	ParseError  int64 `json:"parseError"`
	AntiBot     int64 `json:"antiBot"`
	Transport   int64 `json:"transport"`
}

// indexerBudgetKind is one kind's (query/grab) request-budget standing for the current
// period. limit is the OPERATOR-CONFIGURED cap (0 = none configured); learned is the
// reactive latch harbrr set from the tracker's own quota error, which carries no number.
// Both are reported so the UI can keep a configured cap and a learned one distinct.
type indexerBudgetKind struct {
	Used    int64 `json:"used"`
	Limit   int   `json:"limit"`
	Learned bool  `json:"learned"`
}

// indexerBudget is the per-indexer request-budget block: the current period's counters
// against the configured caps, plus when the period rolls over. It reads the counters
// the registry already keeps (autobrr/harbrr#251) — nothing new is collected.
type indexerBudget struct {
	Unit      string            `json:"unit"`
	PeriodEnd time.Time         `json:"periodEnd"`
	Query     indexerBudgetKind `json:"query"`
	Grab      indexerBudgetKind `json:"grab"`
}

// indexerStatsResponse is the JSON body of the per-indexer stats endpoints: the durable
// query/grab/latency counters plus the failure aggregation. queries counts searches that
// actually reached the tracker (a cache hit bypasses the instrumented path), so
// avgResponseMs reflects real upstream latency. lastQueryAt/lastFailureAt are nil when
// never observed (a never-queried indexer emits null rather than the zero time). budget
// is present on the per-indexer endpoint only; the all-indexers list omits it.
type indexerStatsResponse struct {
	Slug          string               `json:"slug"`
	Queries       int64                `json:"queries"`
	Grabs         int64                `json:"grabs"`
	AvgResponseMs int64                `json:"avgResponseMs"`
	Failures      indexerFailureCounts `json:"failures"`
	LastQueryAt   *time.Time           `json:"lastQueryAt,omitempty"`
	LastFailureAt *time.Time           `json:"lastFailureAt,omitempty"`
	Budget        *indexerBudget       `json:"budget,omitempty"`
}

// indexerStats returns one configured indexer's Prowlarr-style stats. An unknown slug is
// a 404.
func (rt *router) indexerStats(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	st, err := rt.registry.Stats(r.Context(), slug)
	if err != nil {
		rt.writeServiceError(w, "indexer stats", err)
		return
	}
	writeJSON(w, http.StatusOK, toStatsResponse(st))
}

// allIndexerStats returns per-indexer stats for every configured indexer.
func (rt *router) allIndexerStats(w http.ResponseWriter, r *http.Request) {
	stats, err := rt.registry.AllStats(r.Context())
	if err != nil {
		rt.writeServiceError(w, "all indexer stats", err)
		return
	}
	out := make([]indexerStatsResponse, 0, len(stats))
	for _, st := range stats {
		out = append(out, toStatsResponse(st))
	}
	writeJSON(w, http.StatusOK, out)
}

// toStatsResponse maps the registry's stat to its API view, rendering never-observed
// timestamps as nil (JSON null/absent) rather than the zero time.
func toStatsResponse(st registry.IndexerStat) indexerStatsResponse {
	return indexerStatsResponse{
		Slug:          st.Slug,
		Queries:       st.Queries,
		Grabs:         st.Grabs,
		AvgResponseMs: st.AvgResponseMs,
		Failures: indexerFailureCounts{
			AuthFailure: st.Failures.AuthFailure,
			RateLimited: st.Failures.RateLimited,
			ParseError:  st.Failures.ParseError,
			AntiBot:     st.Failures.AntiBot,
			Transport:   st.Failures.Transport,
		},
		LastQueryAt:   zeroToNil(st.LastQueryAt),
		LastFailureAt: zeroToNil(st.LastFailureAt),
		Budget:        toBudgetResponse(st.Budget),
	}
}

// toBudgetResponse maps the registry's budget standing to its API view; nil in
// (AllStats, which does not read it) stays nil out, so the field is simply absent
// rather than a zeroed block that would read as "no budget".
func toBudgetResponse(b *registry.BudgetStatus) *indexerBudget {
	if b == nil {
		return nil
	}
	return &indexerBudget{
		Unit:      b.Unit,
		PeriodEnd: b.PeriodEnd,
		Query:     indexerBudgetKind{Used: b.Query.Used, Limit: b.Query.Limit, Learned: b.Query.Learned},
		Grab:      indexerBudgetKind{Used: b.Grab.Used, Limit: b.Grab.Limit, Learned: b.Grab.Learned},
	}
}

// zeroToNil returns nil for the zero time (never observed) so the JSON field is omitted
// rather than emitting 0001-01-01.
func zeroToNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
