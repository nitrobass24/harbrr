package core

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// IndexerInfo is the indexer identity the Torznab feed needs, sourced from the
// loaded definition. It carries no secrets (no passkeys/cookies/config).
type IndexerInfo struct {
	ID          string
	Name        string
	Description string
	SiteLink    string
	Type        string // "public" / "private" / "semi-private"
	Protocol    string // "torrent" / "usenet"
}

// Indexer is one searchable tracker as the rest of harbrr consumes it: its identity,
// its capabilities (for the caps document and request validation/category mapping),
// and a search entry point that returns normalized releases. It is satisfied by
// an adapter over the Cardigann engine in production and by a fake in
// tests, so a consumer never depends on the concrete engine.
type Indexer interface {
	Info() IndexerInfo
	Capabilities() *mapper.Capabilities
	Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error)
	// NeedsResolver reports whether the definition declares a download block, so a
	// served link must be resolved before a grab. Direct-link trackers report
	// false and their link is served as-is.
	NeedsResolver() bool
	// DownloadNeedsAuth reports whether the download authenticates out-of-band — by
	// session cookie or request header (i.e. the def has a login block) rather than a
	// passkey baked into the URL. Such a link can't be served bare (a bare GET by *arr
	// hits a login page or 401), so it is routed through /dl and fetched with harbrr's
	// authenticated session, just like a resolver-needing link.
	DownloadNeedsAuth() bool
	// Grab performs the grab-time download: resolve the release link (the full
	// Cardigann download algorithm, with testlinktorrent validation) and fetch the
	// torrent through the session, honouring download.method/headers. A magnet is
	// returned as a redirect. The /dl proxy drives this so a passkey-bearing link is
	// resolved and fetched server-side, never exposed in the served feed.
	Grab(ctx context.Context, link string) (*search.GrabResult, error)
	// SupportsOffsetPaging reports whether this Indexer forwards offset/limit upstream
	// for deep-set paging (the newznab and nzbindex usenet drivers, via the flattened
	// registry adapter). It is part of the contract — not a type-asserted optional
	// capability — so the handler and the cache layer read one unconditional signal;
	// every other implementer (every Cardigann def, every other native driver, and the
	// test fakes) answers false.
	SupportsOffsetPaging() bool
	// ConsumesSearchMode reports whether this Indexer routes search.Query.Mode to a
	// different upstream search namespace (newznab/torznab/animebytes, via the
	// flattened registry adapter). The registry's empty-query RSS canonicalization
	// (#341) clears Mode before caching for every non-consuming Indexer — collapsing
	// every RSS consumer's poll onto one cache key regardless of its t= — and leaves
	// it untouched for a consuming Indexer, which needs a per-mode key. Every
	// Cardigann def and every native driver but the three that read Mode answers
	// false.
	ConsumesSearchMode() bool
}

// AggregateSlug is the reserved feed slug that resolves to every enabled indexer
// (autobrr/harbrr#400). It occupies the same {slug} path position as a real indexer,
// so the aggregate feed inherits every Torznab route for free; the registry rejects
// it as an instance slug so the two can never collide.
const AggregateSlug = "all"

// ProfileSlugPrefix marks a feed slug that names a sync profile — "profile:<name>",
// where <name> matches domain.SyncProfile.Name EXACTLY once the path segment is
// URL-decoded (profile names are operator-entered free text, so percent-encoding is
// what carries awkward characters). Like AggregateSlug it sits in the same {slug}
// position and inherits every Torznab route.
//
// It can never collide with an instance slug by construction: registry's slugPattern
// forbids ':', so no instance can be named "profile:anything" (which is also why
// reservedSlugs needs no entry for it). StatusSlugPrefix is the sibling health form.
const ProfileSlugPrefix = "profile:"

// StatusSlugPrefix marks the health-filtered aggregate feed (autobrr/harbrr#400).
// "status:healthy" is the ONLY accepted form: it is `all` minus the indexers harbrr
// currently believes are broken. Any other remainder — "status:failing",
// "status:unknown", a misspelling — is ErrNoSuchFeed, so a typo 404s instead of
// quietly serving an empty feed. Like the other two forms it sits in the same {slug}
// position (inheriting every Torznab route) and cannot collide with an instance slug,
// because registry's slugPattern forbids ':'.
//
// NOTE the semantics, which are LOOSER than the UI's healthy badge: "status:healthy"
// selects every enabled indexer that is NOT failing — healthy AND unknown. The feed's
// job is to skip known-broken indexers, and on a fresh install every indexer derives
// "unknown" until it has been searched or tested, so a literal healthy-only reading
// would serve a brand-new operator an empty feed and permanently exclude
// working-but-never-searched indexers. Disabled instances are excluded, exactly as
// AggregateSlug excludes them.
const StatusSlugPrefix = "status:"

// ErrNoSuchFeed is Provider.Resolve's not-found sentinel: the slug names no indexer,
// no profile, and no reserved aggregate. It is the ONLY error that means "this feed
// does not exist" — any other error from Resolve is a failure to READ the member set
// (the instance store is unreadable), which the serving layer must never render as a
// successful empty feed.
var ErrNoSuchFeed = errors.New("core: no such feed")

// Provider resolves a feed slug to the indexer(s) that serve it.
type Provider interface {
	// Indexer resolves a slug to the single indexer it names. A reserved aggregate
	// slug is NOT an indexer and returns ok=false — which is what binds the /dl grab
	// route to a real member: an aggregate slug can never resolve a download.
	Indexer(ctx context.Context, id string) (Indexer, bool)
	// Resolve returns the member set a feed slug covers, one entry per SELECTED
	// instance: a real indexer resolves to itself, AggregateSlug to every enabled
	// instance, ProfileSlugPrefix to the profile's members (a disabled member
	// included, as a skip), StatusSlugPrefix to the enabled instances that are not
	// failing. Every returned member is either live (Indexer != nil) or
	// carries a constant skip Reason, so nothing a slug selects is ever silently
	// absent from the served ledger.
	//
	// An aggregate or profile with no members resolves to an empty set and a nil error
	// — a valid, empty feed. ErrNoSuchFeed means the slug names nothing; any other
	// error means the member set could not be READ and must not be served as empty.
	Resolve(ctx context.Context, slug string) ([]MemberOutcome, error)
}
