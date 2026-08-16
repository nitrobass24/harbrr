# Pagination

harbrr returns search results a page at a time, and it does so carefully. If you've ever
paged through a Torznab feed and seen the same release show up on two different pages, or a
`total` that couldn't be squared with what you were handed, those are exactly the failure
modes harbrr is built to avoid.

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

  `total` is counted **after** dedupe/category-filtering but **before** the page slice, so it
  can be larger than `results.length` — see the next section for what it counts on a tracker
  that pages upstream. `hasMore` is simply `offset + len(results) < total`.

- **Stable, disjoint pages.** Walking a query page by page yields each release **exactly
  once**. harbrr pins this with standing tests (`TestFeedCrossPageNoDuplicate`,
  `TestSearchReleasesCrossPageDisjoint`, `TestSearchReleasesTotalIsHonest`).

### What `total` means depends on how the indexer pages

- **Local-slice indexers** (the majority — see [below](#upstream-paging)) hand harbrr the
  whole result set, so `total` is the real match count and **stays put** across the walk.
- **Upstream-paging indexers** don't tell harbrr a grand total — the API simply hands back a
  page. There `total` is a deliberate **running floor**: while a page comes back full it
  reports `offset + limit + 1`, meaning "at least one more page", and it settles on the exact
  `offset + len(results)` when a short page proves you've hit the end. So `total` *grows* as
  you walk, on purpose: it's what tells your \*arr to ask for the next page.

In both cases `hasMore` is the flag to trust when you just want to know whether to keep going.

## Page window

- `limit` and `offset` are query params on both the feed and the JSON search.
- Page size is **default = max = 100**. A larger `limit` is clamped down rather than rejected;
  an out-of-range `offset` simply yields an empty page. This lenient clamping is deliberate —
  harbrr never answers a paging request with a spec-201 error.

## Conditional GET is paging-aware

The feed's [`ETag` / `If-None-Match`](search-results-cache.md#conditional-requests-etag--if-none-match)
revalidation folds the **page window** into the validator, so a `304 Not Modified` for one
page can never be answered with another page's body. Each page revalidates independently and
correctly.

## Upstream paging

Where a tracker's own API takes a page window, harbrr forwards `offset`/`limit` upstream, so
asking for page 2 fetches the tracker's page 2 rather than re-slicing page 1. That covers:

- **Newznab indexers** (every Usenet preset), which take `offset`/`limit` directly.
- **NZBIndex**, which pages via `p=`.
- **Nebulance**, which takes `page`/`per_page`.
- **Gazelle drivers on sites with a fixed upstream page size** — AlphaRatio today; Redacted,
  Orpheus, and BrokenStones return their results in one shot and stay on the local-slice path.

Everything else — every Cardigann definition and the remaining native drivers — is
**single-fetch**: one engine fetch backs every page of a query, and harbrr slices it locally.
For those, page 2 can only contain what the tracker returned the first time.
