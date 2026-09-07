# The indexer serving contract lives in internal/indexer/core, not the web layer

The `Indexer` interface — a searchable tracker as harbrr consumes it (`Info`/
`Capabilities`/`Search`/`Grab`/`NeedsResolver`/`DownloadNeedsAuth`/
`SupportsOffsetPaging`) — together with `Provider`, the `IndexerInfo`/`CacheInfo`
DTOs, the cache-sink context plumbing, and the shared `SearchReleases` read
pipeline, now lives in `internal/indexer/core`. `registry` (which builds and
adapts indexers) and `web/torznabhttp` (which serves them over Torznab/Newznab)
both depend on `core`. Previously all of it lived in `web/torznabhttp`, forcing
`registry → web/torznabhttp`: the producer of indexers imported the HTTP-serving
package to name its own central abstraction.

The load-bearing choice: the indexer serving contract is a **domain concept, not
a web concept**, so it lives at the indexer subsystem's altitude — above the
Cardigann engine, below any transport — and `web/torznabhttp` is reduced to pure
HTTP/XML serving over that contract.

## Considered options

- **Keep it in `web/torznabhttp` (consumer-defined interface).** The Go idiom of
  defining an interface in its consumer fits a *single* consumer. `Indexer` has
  multiple implementers (Cardigann adapter, native-driver adapters, fakes) and
  multiple consumers (feed handler, registry, JSON search API), so it is a shared
  contract, and homing it in one consumer inverts the layering (registry → web).
- **Put it in `internal/domain`.** Rejected: the contract references engine types
  (`mapper.Capabilities`, `normalizer.Release`, `search.Query`/`GrabResult`);
  `domain` is kept dependency-light. `core` sits at the right altitude and may
  depend on the engine's shared types.
- **Extract `internal/indexer/core`.** Chosen.

## Consequences

- `registry → web/torznabhttp` is removed; both point at `core`. `core` depends
  on `cardigann/{mapper,normalizer,search}`; no cycle (those packages don't know
  the contract).
- The handler still depends only on the `Indexer` interface, so test fakes and the
  "handler never depends on the concrete engine" property are preserved.
- `CacheInfo` and its sink plumbing move too, already in its reshaped
  `{Cached bool, ExpiresAt}` form: the `ETag → Cached bool` reshape (#173) landed
  on the stack first, so this move carries the finalized type rather than
  coordinating with it in flight.
- "Indexer" enters `CONTEXT.md` as ubiquitous language.
- The exact `core` boundary — whether `SearchReleases` moves wholesale — is settled
  in implementation, guided by "core = the indexer as consumed; torznabhttp = HTTP
  serving."

## Amendment: the Grab seam moves out too (autobrr/harbrr#552)

Moving the contract to `core` left `web/torznabhttp` smaller but not yet what this
decision claimed it was. Sixteen of its twenty-two exported symbols had nothing to do
with the Torznab feed: the `/dl` token codec, the link-sealing rewriters, the
resolve-and-stream core, and the `/dl` URL derivation — the Grab seam, which is
indexer domain wearing the feed's name. The tell was `internal/app/adapters.go`,
where the announce background push — a code path with no `http.Request` anywhere in
it — imported a package named for the *arr wire protocol purely to mint a download
token. So `internal/indexer/grab` was extracted, at the same altitude and for the
same reason `core` was: the Grab seam is a domain concept, not a web one, and the
invariant it holds ("a passkey never leaves harbrr") deserves one module and one test
suite rather than being spread across the feed handler and its callers. This
**completes** the reduction this ADR set out to make — `web/torznabhttp` is now the
pure HTTP/XML feed serving it was scoped to be, with `/dl` as a thin route adapter
over `grab.ServeGrab` — rather than revising it. `FeedURL` stayed behind, since a
Torznab results-feed URL is feed domain; it takes `grab.URLConfig` so the feed URL and
the `/dl` URLs it emits cannot disagree about where harbrr is. "Grab" was already
ubiquitous language in `CONTEXT.md`; it now names a package as well.
