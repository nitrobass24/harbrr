package torznabhttp

import (
	"net/http"
	"net/url"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/core"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// aggregateDescription is the <description> of every aggregate feed. It is deliberately
// static (no member count): the channel metadata must not churn per request.
const aggregateDescription = "harbrr aggregate feed"

// skipUnsupportedMode is the ledger reason for a member dropped before the fan-out
// because it does not advertise the requested search mode (or an id param the mode
// needs). It is a capability skip, not a failure: the request still succeeds on the
// members that do support the mode, which is the whole point of a partial aggregate.
const skipUnsupportedMode = "unsupported-mode"

// serveAggregate answers the aggregate ('all') feed: caps are the union of member
// capabilities, results are a partial fan-out. It never fails on a member — the request
// only fails if the request itself is invalid (a bad apikey, an unknown t=, a mode NO
// member supports).
func (h *handler) serveAggregate(w http.ResponseWriter, r *http.Request, slug string, members []core.Indexer, q url.Values) {
	caps := core.UnionCapabilities(members)
	if strings.EqualFold(q.Get("t"), tzn.ReqCaps) {
		h.writeCapsDoc(w, slug, caps)
		return
	}
	h.writeAggregateResults(w, r, slug, members, caps, q)
}

// writeAggregateResults gates the search mode against the union caps, fans out to the
// members that can actually serve it, and renders the merged window plus the ledger.
func (h *handler) writeAggregateResults(w http.ResponseWriter, r *http.Request, slug string, members []core.Indexer, caps *mapper.Capabilities, q url.Values) {
	capsKey, ok := h.resolveMode(w, q, caps)
	if !ok {
		return
	}
	eligible, skipped := partitionByMode(members, capsKey, q)
	// No CacheInfo sink is attached: members run concurrently and would race on one
	// sink, and a merged window has no single expiry to hang a validator off. The
	// SEARCH cache still applies per member inside SearchReleases — the aggregate costs
	// the trackers exactly what the per-indexer polls would — it is only the HTTP-level
	// 304 short-circuit the aggregate feed forgoes.
	ctx := r.Context()
	if requestNoCache(r) {
		ctx = core.WithCacheBypass(ctx)
	}
	res := core.SearchAggregate(ctx, eligible, q)
	h.logMemberFailures(res.Members)
	body, err := tzn.MarshalAggregateResults(
		h.aggregateFeedInfo(r, slug),
		h.aggregateItems(r, res.Releases),
		memberLedger(res.Members, skipped),
		tzn.Page{Offset: res.Offset, Total: res.Total},
		h.clock(),
	)
	if err != nil {
		h.writeInternalError(w, "results", slug, err)
		return
	}
	writeXML(w, http.StatusOK, body)
}

// partitionByMode splits members into those that advertise the requested mode (and the
// id params it was asked with) and those that do not. The latter are recorded as
// capability skips rather than dropped silently, so the ledger explains a short answer.
func partitionByMode(members []core.Indexer, capsKey string, q url.Values) (eligible []core.Indexer, skipped []core.MemberOutcome) {
	for _, m := range members {
		caps := m.Capabilities()
		_, paramOK := unsupportedIDParam(caps, capsKey, q)
		if tzn.ModeAvailable(caps, capsKey) && paramOK {
			eligible = append(eligible, m)
			continue
		}
		skipped = append(skipped, core.MemberOutcome{Indexer: m, Reason: skipUnsupportedMode})
	}
	return eligible, skipped
}

// memberLedger renders the served status ledger from the fan-out outcomes plus the
// members skipped before it. Only the closed reason vocabulary crosses over —
// MemberOutcome.Err is for the log, never the wire.
func memberLedger(outcomes, skipped []core.MemberOutcome) []tzn.MemberStatus {
	out := make([]tzn.MemberStatus, 0, len(outcomes)+len(skipped))
	for _, o := range outcomes {
		out = append(out, memberStatus(o))
	}
	for _, o := range skipped {
		out = append(out, memberStatus(o))
	}
	return out
}

func memberStatus(o core.MemberOutcome) tzn.MemberStatus {
	status := "skipped"
	if o.OK() {
		status = "ok"
	}
	info := o.Indexer.Info()
	return tzn.MemberStatus{ID: info.ID, Name: info.Name, Status: status, Reason: o.Reason, Count: o.Count}
}

// logMemberFailures records each member failure with the error redacted. This is the
// ONLY place a member's raw failure is reported, and it goes to the log, not the feed.
func (h *handler) logMemberFailures(outcomes []core.MemberOutcome) {
	for _, o := range outcomes {
		if o.Err == nil {
			continue
		}
		h.log.Warn().
			Str("stage", "aggregate").
			Str("indexer", o.Indexer.Info().ID).
			Str("reason", o.Reason).
			Str("error", apphttp.RedactError(o.Err)).
			Msg("aggregate feed: member contributed nothing")
	}
}

// aggregateItems pairs every merged release with its ORIGIN member's feed identity and
// /dl rewriter. This is what binds a grab to the tracker the release came from: the
// enclosure URL points at /api/indexers/<member>/dl with a token sealed against that
// member, never at the aggregate slug (which resolves no indexer and therefore 404s).
// The per-member identity/rewriter pair is built once, not per release.
func (h *handler) aggregateItems(r *http.Request, releases []core.AggregateRelease) []tzn.AggregateItem {
	type origin struct {
		feed    tzn.FeedInfo
		rewrite tzn.AcquisitionRewriter
	}
	origins := map[string]origin{}
	out := make([]tzn.AggregateItem, 0, len(releases))
	for _, rel := range releases {
		id := rel.Indexer.Info().ID
		o, ok := origins[id]
		if !ok {
			o = origin{feed: h.feedInfo(r, rel.Indexer), rewrite: h.dlRewriter(r, rel.Indexer)}
			origins[id] = o
		}
		out = append(out, tzn.AggregateItem{Feed: o.feed, Release: rel.Release, Rewrite: o.rewrite})
	}
	return out
}

// aggregateFeedInfo is the channel envelope for an aggregate feed. Only the channel
// metadata comes from here — every ITEM is rendered under its own member's identity —
// so Type/Protocol are deliberately unset: an aggregate spans both protocols and must
// not assert one at the channel level.
func (h *handler) aggregateFeedInfo(r *http.Request, slug string) tzn.FeedInfo {
	return tzn.FeedInfo{
		IndexerID:   slug,
		Name:        "harbrr (" + slug + ")",
		Description: aggregateDescription,
		SelfURL:     h.selfURL(r),
	}
}
