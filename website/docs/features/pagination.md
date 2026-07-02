# Pagination

harbrr returns search results a page at a time, and it does so carefully. If you've ever
paged through a Torznab feed and seen the same release show up on two different pages — or a
`total` that changed underneath you mid-walk — those are exactly the failure modes harbrr is
built to avoid.

Most operators never touch pagination directly; your apps handle it. But it matters whenever
something walks the feed page by page (a manual search UI, a script, cross-seed), so here's
what harbrr guarantees.

---

## What you get

- **Honest counts.** The Torznab/Newznab feed emits `<newznab:response offset="…"
  total="…">` on every search, so a client always knows where it is and how many matches
  exist.
- **A real JSON envelope.** The JSON search endpoint
  (`GET /api/indexers/{slug}/search`) wraps results in a qui-shaped envelope instead of a bare
  array:

  ```json
  {
    "results": [ /* this page's releases */ ],
    "total": 87,        // full match count, before this page's slice
    "hasMore": true,    // are there results past this page?
    "limit": 100,       // resolved page size
    "offset": 0         // resolved page offset
  }
  ```

  `total` is the count **after** dedupe/category-filtering but **before** the page slice, so
  it can be larger than `results.length`. `hasMore` is simply `offset + len(results) < total`.

- **Stable, disjoint pages.** Walking a query page by page yields each release **exactly
  once**, and `total` stays put across the walk. harbrr pins this with standing tests
  (`TestFeedCrossPageNoDuplicate`, `TestSearchReleasesCrossPageDisjoint`,
  `TestSearchReleasesTotalIsHonest`).

## Page window

- `limit` and `offset` are query params on both the feed and the JSON search.
- Page size is **default = max = 100**. A larger `limit` is clamped down rather than rejected;
  an out-of-range `offset` simply yields an empty page. This lenient clamping is deliberate —
  harbrr never answers a paging request with a spec-201 error.

## Conditional GET is paging-aware

The feed's [`ETag` / `If-None-Match`](search-results-cache.md#conditional-requests-etag-if-none-match)
revalidation folds the **page window** into the validator, so a `304 Not Modified` for one
page can never be answered with another page's body. Each page revalidates independently and
correctly.

!!! note "Single-fetch `total` (for now)"
    `total` reflects the **one** engine fetch that backs every page of a query — harbrr does
    not yet fan out across an upstream tracker's own page 2, 3, … on your behalf. Deep
    server-side multi-page upstream fetching is tracked post-alpha
    ([#75](https://github.com/nitrobass24/harbrr/issues/75)); when it lands, paginating
    trackers will return genuine page-2+ results while keeping every guarantee above.
