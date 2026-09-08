package core

import (
	"slices"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// filterResults drops releases whose categories do not match the requested
// newznab categories, reproducing the category block of Jackett's
// BaseIndexer.FilterResults:
//
//	if (query.Categories.Length > 0) {
//	    var expanded = TorznabCaps.Categories.ExpandTorznabQueryCategories(query);
//	    results = results.Where(r =>
//	        r.Category?.Any() != true || expanded.Intersect(r.Category).Any());
//	}
//
// So: with no requested categories every release passes; otherwise a release is
// kept when it carries NO categories OR its categories intersect the expanded
// requested set. requestedCats are the raw newznab ids from the `cat` param;
// Release.Categories are already newznab ids. (Jackett's IsValidRelease and the
// result-limit Take live elsewhere in harbrr's pipeline — paging and the engine.)
func filterResults(releases []*normalizer.Release, requestedCats []int, caps *mapper.Capabilities) []*normalizer.Release {
	if len(requestedCats) == 0 {
		return releases
	}
	expanded := caps.ExpandQueryCategories(requestedCats)
	wanted := func(c int) bool { return slices.Contains(expanded, c) }
	out := make([]*normalizer.Release, 0, len(releases))
	for _, r := range releases {
		// r == nil short-circuits before r.Categories (kept; MarshalResults skips
		// nil). A release with no categories is kept (Jackett's `!Any()` branch).
		if r == nil || len(r.Categories) == 0 || slices.ContainsFunc(r.Categories, wanted) {
			out = append(out, r)
		}
	}
	return out
}
