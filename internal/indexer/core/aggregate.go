package core

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fanoutLimit bounds how many member searches run at once. Per-host pacing, the
// request budget and the circuit breaker already live inside each member's Search, so
// this is only a goroutine/fd ceiling, not the tracker-load control.
const fanoutLimit = 8

// Member skip reasons for the aggregate status ledger. They are a CLOSED vocabulary of
// constants precisely so the ledger can never carry a raw error string: a member's
// failure detail routinely embeds a passkey-bearing URL, and the ledger is served to
// the consumer. The redacted specifics go to the log, never here.
const (
	// SkipBudget: the member's request budget for this period is spent.
	SkipBudget = "budget-exhausted"
	// SkipCircuit: the member's circuit breaker is open, so harbrr did not call it.
	SkipCircuit = "circuit-open"
	// SkipDegenerateQuery: the member's own keywordsfilters leave nothing of this
	// query to search on, so harbrr did not ask it (autobrr/harbrr#394).
	SkipDegenerateQuery = "degenerate-query"
	// SkipRateLimited: the tracker rate-limited us or reported its quota exceeded.
	SkipRateLimited = "rate-limited"
	// SkipUnreachable: a gateway/CDN reported the tracker's origin unreachable.
	SkipUnreachable = "unreachable"
	// SkipTimeout: the member did not answer within the request's deadline.
	SkipTimeout = "timeout"
	// SkipError: any other member failure (details are logged, redacted).
	SkipError = "error"
	// SkipDisabled: the member is a disabled instance. Only a profile can select one
	// (AggregateSlug covers enabled instances only); it is ledgered rather than dropped
	// so a profile feed explains why a member the operator put in it served nothing.
	SkipDisabled = "disabled"
	// SkipUnavailable: harbrr could not BUILD this member's engine (corrupt stored
	// config, a definition that vanished under it), so it never reached the fan-out.
	// The redacted cause is logged by the resolver; only this constant is served.
	SkipUnavailable = "unavailable"
)

// MemberOutcome is one member of an aggregate feed's member set, carried from
// RESOLUTION through the fan-out into the served per-indexer status ledger. One exists
// for every instance the slug SELECTS — including members that never ran — which is
// what makes the ledger's "absence is visible" promise hold end to end.
//
// Reason is "" while the member is still live: Indexer is set and Count says how many
// releases it put into the merged set (BEFORE the page slice). Otherwise Reason is one
// of the Skip* constants and Count is 0 — set either at resolution (disabled,
// unavailable), before the fan-out (an unsupported search mode) or by the fan-out
// itself.
type MemberOutcome struct {
	// ID and Name identify the member in the ledger. They are fixed at RESOLUTION and
	// are the only identity source the ledger has: a member skipped before it was built
	// has no Indexer to ask.
	ID   string
	Name string
	// Indexer is the live member, nil once the member is skipped.
	Indexer Indexer
	Reason  string
	Count   int
	// Err is the underlying failure, for the serving layer to LOG (redacted) — it is
	// never part of the ledger a consumer sees. Reason is the only failure detail that
	// is ever served, precisely because an engine error routinely embeds a
	// passkey-bearing URL.
	Err error
}

// OK reports whether the member is still live — i.e. it has not been skipped, so it
// is searched by the fan-out and (afterwards) contributed to the merged set. It
// requires the Indexer to actually be present: Provider is an exported interface, so
// an implementer that hand-builds an outcome (bypassing LiveMember/SkippedMember) must
// not be able to send the fan-out a nil Indexer that reads as searchable — a zero
// MemberOutcome is a skip, never a panic on the serving path.
func (m MemberOutcome) OK() bool { return m.Reason == "" && m.Indexer != nil }

// LiveMember builds the resolved-and-ready member for idx, taking its ledger identity
// from the indexer itself. It is how every caller that already holds an Indexer enters
// the member set.
func LiveMember(idx Indexer) MemberOutcome {
	info := idx.Info()
	return MemberOutcome{ID: info.ID, Name: info.Name, Indexer: idx}
}

// SkippedMember builds a member that will never be searched, identified by the stored
// instance row (slug + name) because there is no engine to ask. reason must be one of
// the Skip* constants — never a raw error.
func SkippedMember(id, name, reason string) MemberOutcome {
	return MemberOutcome{ID: id, Name: name, Reason: reason}
}

// Skip returns a copy of m marked as skipped for reason, dropping the Indexer so a
// later stage cannot search a member the ledger already reports as skipped.
func (m MemberOutcome) Skip(reason string) MemberOutcome {
	m.Indexer, m.Reason, m.Count = nil, reason, 0
	return m
}

// AggregateRelease is a release plus the member it came from. The origin travels WITH
// the release all the way to serialization: that is what makes an aggregate grab bind
// to the tracker the release actually came from rather than to the aggregate slug.
type AggregateRelease struct {
	Indexer Indexer
	Release *normalizer.Release
}

// AggregateResult is the merged, paged output of a fan-out plus the ledger that says
// which members are behind it. Total is the size of the merged set this request
// actually fetched — a floor a consumer can stand behind, never a sum of member
// claims, and never a promise that a deeper page exists.
type AggregateResult struct {
	Releases []AggregateRelease
	Members  []MemberOutcome
	Total    int
	Offset   int
	Limit    int
}

// SearchAggregate fans one Torznab request out across members and merges the answers.
//
// It is partial by construction: every LIVE member runs through the SAME SearchReleases
// pipeline a per-indexer feed uses — so its search cache, request budget, circuit
// breaker, per-host pacing and freeleech view all apply unchanged — and a member that
// errors, times out, is budget-exhausted or is breaker-open simply contributes nothing.
// No member failure can fail the request. A member that arrives already skipped is
// passed through untouched, never searched. The returned Members ledger is in the
// caller's member order and always has one entry per member.
//
// PAGING IS ONE WINDOW. Members are queried at offset 0 with the maximum page size and
// the request's limit/offset are applied to the MERGED set, because per-indexer offsets
// cannot be preserved honestly across a merge (autobrr/harbrr#400 — deep paging over an
// aggregate is explicitly out of scope). Pinning the member window to offset 0 also
// keeps the fan-out on the same cache key a plain per-indexer RSS poll uses, so an
// aggregate poll costs the trackers nothing extra.
func SearchAggregate(ctx context.Context, members []MemberOutcome, q url.Values) AggregateResult {
	pg := parsePaging(q)
	fetched := fanOut(ctx, members, memberQuery(q))
	merged := mergeByPublishDate(members, fetched)
	return AggregateResult{
		Releases: pg.applyAggregate(merged),
		Members:  ledger(members, fetched),
		Total:    len(merged),
		Offset:   pg.offset,
		Limit:    pg.limit,
	}
}

// memberFetch is one member's fan-out slot, written by exactly one goroutine.
type memberFetch struct {
	releases []*normalizer.Release
	reason   string
	err      error
}

// fanOut runs every LIVE member's search concurrently (bounded by fanoutLimit) and
// returns one slot per member, positionally; an already-skipped member keeps a zero slot
// and is never searched. Each goroutine owns its own slot, so no lock is needed; no
// goroutine ever returns an error, so one member's failure can neither cancel a sibling
// nor abort the wait.
func fanOut(ctx context.Context, members []MemberOutcome, q url.Values) []memberFetch {
	out := make([]memberFetch, len(members))
	var g errgroup.Group
	g.SetLimit(fanoutLimit)
	for i, m := range members {
		if !m.OK() {
			continue
		}
		g.Go(func() error {
			res, err := SearchReleases(ctx, m.Indexer, q)
			if err != nil {
				out[i] = memberFetch{reason: classifySkip(err), err: err}
				return nil
			}
			out[i] = memberFetch{releases: res.Releases}
			return nil
		})
	}
	_ = g.Wait() // no goroutine returns an error; failures live in the slots
	return out
}

// ledger folds the fan-out slots back onto the member set to produce the served status
// ledger: one row per SELECTED member, in resolution order. A member skipped before the
// fan-out keeps the reason it arrived with (its slot is empty, so reading it would
// silently clear the skip).
func ledger(members []MemberOutcome, fetched []memberFetch) []MemberOutcome {
	out := make([]MemberOutcome, len(members))
	for i, m := range members {
		out[i] = m
		if !m.OK() {
			continue
		}
		out[i].Reason, out[i].Err, out[i].Count = fetched[i].reason, fetched[i].err, len(fetched[i].releases)
	}
	return out
}

// classifySkip maps a member failure to a ledger reason. The self-imposed gates come
// first: "harbrr chose not to ask" is the fact an operator most needs to tell apart
// from a tracker outage. A cancelled context is the CONSUMER hanging up, so it is
// reported as a timeout rather than a tracker fault.
func classifySkip(err error) string {
	switch {
	case errors.Is(err, ErrBudgetExhausted):
		return SkipBudget
	case errors.Is(err, ErrCircuitOpen):
		return SkipCircuit
	case errors.Is(err, ErrDegenerateQuery):
		return SkipDegenerateQuery
	case errors.Is(err, search.ErrRateLimited), errors.Is(err, search.ErrQuotaExceeded):
		return SkipRateLimited
	case errors.Is(err, search.ErrGatewayStatus):
		return SkipUnreachable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return SkipTimeout
	default:
		return SkipError
	}
}

// memberQuery is the request as each member sees it: the caller's params with the page
// window pinned to the first full page. The merged set is paged afterwards (see
// SearchAggregate), and pinning the window here is what keeps a member's cache key
// identical to its own RSS poll's.
func memberQuery(q url.Values) url.Values {
	mq := make(url.Values, len(q)+2)
	for k, v := range q {
		mq[k] = v
	}
	mq.Set("offset", "0")
	mq.Set("limit", strconv.Itoa(defaultLimit))
	return mq
}

// mergeByPublishDate flattens the fan-out into one publish-date-descending set — the
// RSS convention, and the only ordering that is meaningful across trackers. Ties and
// undated releases keep member order (the sort is stable and an unparseable date sorts
// to the zero time, i.e. last), so the merged feed is deterministic.
func mergeByPublishDate(members []MemberOutcome, fetched []memberFetch) []AggregateRelease {
	var merged []AggregateRelease
	for i, m := range members {
		for _, rel := range fetched[i].releases {
			if rel != nil {
				merged = append(merged, AggregateRelease{Indexer: m.Indexer, Release: rel})
			}
		}
	}
	slices.SortStableFunc(merged, func(a, b AggregateRelease) int {
		return publishedAt(b.Release).Compare(publishedAt(a.Release))
	})
	return merged
}

// publishedAt parses a release's RFC3339 publish date; anything unparseable is the zero
// time, which sorts last in the descending merge.
func publishedAt(r *normalizer.Release) time.Time {
	t, err := time.Parse(time.RFC3339, r.PublishDate)
	if err != nil {
		return time.Time{}
	}
	return t
}

// applyAggregate is paging.apply for the merged set.
func (p paging) applyAggregate(releases []AggregateRelease) []AggregateRelease {
	if p.offset >= len(releases) {
		return nil
	}
	return releases[p.offset:min(p.offset+p.limit, len(releases))]
}

// UnionCapabilities merges member capabilities into the caps an aggregate feed
// advertises: a search mode is available if ANY member offers it, a mode's supported
// params are the union of the members that offer it, the flags are OR-ed, and the
// category list is the deduplicated union in the ascending-id order the serializer
// expects. It carries no CategoryMap or DefaultCategories on purpose — category
// mapping is per-tracker and each member does its own inside SearchReleases; the union
// exists only to answer t=caps and to gate the requested search mode. A member already
// skipped at resolution has no engine to ask and contributes nothing: an unbuildable or
// disabled member must not narrow (or widen) what the feed advertises.
func UnionCapabilities(members []MemberOutcome) *mapper.Capabilities {
	union := &mapper.Capabilities{Modes: map[string][]string{}}
	cats := map[int]mapper.Category{}
	for _, m := range members {
		if !m.OK() {
			continue
		}
		caps := m.Indexer.Capabilities()
		if caps == nil {
			continue
		}
		union.AllowRawSearch = union.AllowRawSearch || caps.AllowRawSearch
		union.AllowTVSearchIMDB = union.AllowTVSearchIMDB || caps.AllowTVSearchIMDB
		for mode, params := range caps.Modes {
			union.Modes[mode] = unionParams(union.Modes[mode], params)
		}
		for _, c := range caps.Categories {
			cats[c.ID] = c
		}
	}
	union.Categories = sortedCategories(cats)
	return union
}

// unionParams appends the params of have that dst does not already carry, preserving
// first-seen order. A mode declared with an empty param list still registers the mode
// key (as an empty, therefore unavailable, entry) exactly as it does per indexer.
func unionParams(dst, have []string) []string {
	if dst == nil {
		dst = []string{}
	}
	for _, p := range have {
		if !slices.Contains(dst, p) {
			dst = append(dst, p)
		}
	}
	return dst
}

// sortedCategories flattens the deduplicated category union into the ascending-id order
// mapper.Capabilities.Categories documents (the serializer re-derives the tree from it).
func sortedCategories(cats map[int]mapper.Category) []mapper.Category {
	out := make([]mapper.Category, 0, len(cats))
	for _, c := range cats {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b mapper.Category) int { return a.ID - b.ID })
	return out
}
