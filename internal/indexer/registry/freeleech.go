package registry

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// freeleechIndexer is the OUTERMOST torznabhttp.Indexer decorator: it applies the
// per-indexer "freeleech only" view at SERVE time, over the full result set the
// engine fetched and the cache stored. Keeping the filter outside the cache is what
// lets one tracker fetch serve both the honor feed (freeleech-only, for the *arrs)
// and the bypass feed (full catalog, for qui/cross-seed) from a SINGLE cached entry —
// so a later bypass poll never re-hits the tracker just because an *arr polled FL-only
// first.
//
// freeleechOnly is the instance's stored `freeleech` setting. The engine itself is
// built with that key cleared (buildAdapter), so it always returns the full catalog;
// this decorator is the only place freeleech narrows the result. The bypass feed
// variant sets query.FreeleechBypass to skip the filter entirely.
type freeleechIndexer struct {
	torznabhttp.Indexer
	freeleechOnly bool
}

// SupportsOffsetPaging delegates the OffsetPager capability to the wrapped indexer.
// Like cachedIndexer, this MUST be hand-written: embedding the torznabhttp.Indexer
// INTERFACE does not promote a method absent from it, so without this the handler
// would not see a deep-paging driver through this wrapper and would double-offset.
func (f *freeleechIndexer) SupportsOffsetPaging() bool {
	return supportsOffsetPaging(f.Indexer)
}

// Search returns the full result set unless this indexer is in freeleech-only mode AND
// the request is not the bypass variant, in which case it keeps only freeleech releases.
// The freeleech signal is downloadVolumeFactor == 0 — the per-row marker every freeleech
// def stamps independent of the setting (see normalizer.Release.DownloadVolumeFactor).
//
// Paging note: this filter runs INSIDE the Search the handler's pager measures, so on a
// deep-paging driver the honor feed's has-more floor is computed on the post-filter page
// and can stop early (the documented pagination-dilution divergence). This is unreachable
// for the shipped paging driver — only the newznab/usenet driver forwards offset upstream,
// and usenet has no freeleech setting, so freeleechOnly is always false there.
func (f *freeleechIndexer) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	releases, err := f.Indexer.Search(ctx, q)
	if err != nil || !f.freeleechOnly || q.FreeleechBypass {
		return releases, err //nolint:wrapcheck // passthrough; the adapter already wraps with the indexer id.
	}
	return filterFreeleechOnly(releases), nil
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
