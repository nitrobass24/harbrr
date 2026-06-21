# Torznab serializer fixtures

Golden XML for harbrr's Torznab/Newznab serializer (`internal/torznab`), the
*arr-facing contract. Each golden is harbrr's own canonical, deterministic
output and is byte-compared by the package tests; regenerate with
`go test ./internal/torznab/ -update` only after confirming the output matches
the case's oracle.

## Oracle policy (offline)

Goldens are **not** captured from a live Jackett or a live Sonarr/Radarr (the
project decision; see `../../indexer/cardigann/parity/testdata/README.md`). harbrr
is GPL-2.0, same as Jackett, so porting Jackett's own test material is
license-compatible (`jackett/NOTICE`). Each golden records its `golden_source`:

- **`jackett-port`** — values ported from Jackett's own test assertions
  (`CardigannIndexerTests.TestCardigannTorznabCategories`) or its serializer
  source (`TorznabCapabilities`, `ResultPage`), at the pinned commit
  `b4140c7`. The authoritative offline oracle.
- **`hand-derived`** — values computed by hand from the Torznab/Newznab spec +
  Jackett's serializer semantics + the Sonarr/Radarr request shapes.

## `caps/` — capabilities document (`t=caps`)

| file | golden_source | what it pins |
|------|---------------|--------------|
| `caps/jackett-categories.xml` | jackett-port | The category tree from `TestCardigannTorznabCategories`' 2nd definition: parent/child nesting, custom ids (100044, **137107**, 100045), and the `GetTorznabCategoryTree(true)` sort (standard parents ascending by id, then customs by name). |
| `caps/jackett-modes.xml` | jackett-port | The re-derived `supportedParams` for the 3rd definition: tv-search drops `imdbid` (gated by `AllowTVSearchIMDB`, off here), `audio-search` mirrors `music-search`, all six modes always emitted. |
| `caps/allowrawsearch.xml` | hand-derived | `allowrawsearch` adds `searchEngine="raw"` to every mode; `allowtvsearchimdb: true` makes tv-search advertise `imdbid` (in canonical order `q,season,imdbid`). |

The structural facts behind the `jackett-port` goldens (custom-id hashes, tree
order, supported-param order) are additionally asserted directly in
`../caps_test.go` (`TestCapsCategoryTreeOracle`, `TestCapsSupportedParamsOracle`,
`TestCapsTVImdbGate`), independent of XML whitespace.

## `results/` — results feed (`t=search` and the typed modes)

| file | golden_source | what it pins |
|------|---------------|--------------|
| `results/feed.xml` | hand-derived | The `<item>` element order + `torznab:attr` block from `ResultPage.ToXml`: standard + custom category emission (plain `<category>` and `torznab:attr`), `imdb` (7-digit) vs `imdbid` (`tt`+7-digit), freeleech `downloadvolumefactor=0`, a magnet-only release (guid/link/enclosure fall back to the magnet; `&`→`&amp;`; `<size>0</size>`/`length="0"`), and control-char stripping. |
| `results/empty.xml` | hand-derived | A no-results feed: a valid `<rss>`/`<channel>` with the full header and zero `<item>`s (HTTP 200, never an error). |

The `<item>` grammar (element/attr names + order, RFC1123Z `pubDate`, guid
precedence) is reproduced from `ResultPage.ToXml` / `ReleaseInfo` /
`BaseIndexer.FixResults` at commit `b4140c7`.

## Known divergences from Jackett / the spec

Deliberate or accepted differences, each with an explicit disposition
(`[Tracked]` a real gap with a plan follow-up · `[Resolved]` a once-tracked gap
now closed · `[Deliberate]` an intentional design choice · `[Accepted]` a kept
difference, no work planned).
None is hidden by a fixture authored to dodge it.

**Scope:** this section covers the **Torznab output** layer (normalized release →
served XML + the *arr HTTP handler). Engine-side differences (extraction,
login, request building, parsing) live in
[`../../indexer/cardigann/parity/testdata/README.md`](../../indexer/cardigann/parity/testdata/README.md).
[`docs/divergences.md`](../../../docs/divergences.md) is the single index of both
and the shared disposition rule.

### Caps document

- **`<server title="harbrr">`** — Jackett emits `title="Jackett"`. Cosmetic;
  Sonarr/Radarr ignore it. **`[Deliberate]`**
- **Attribute-only elements render `<e></e>` not `<e/>`** — harbrr uses
  `encoding/xml`, which has no self-closing form; both are well-formed and parse
  identically for *arr. **`[Deliberate]`**
- **Duplicate-custom-id `<category>` collapse** — when two category mappings
  resolve to the same custom id (numeric reuse or a SHA1-uint16 hash collision),
  Jackett's tree carries both `<category>` nodes; harbrr's mapper de-dups
  advertised categories by id (last name wins), so harbrr emits one. A subset of
  the 558 vendored defs do this. *arr keys categories by id, so a duplicate id
  with a second name is cosmetic. **`[Accepted]`**
- **Custom-category top-level ordering** — Jackett sorts the top-level tree with
  C# `OrderBy` over the `"zzz"+Name` key, which is **CurrentCulture** (linguistic)
  string comparison; harbrr uses Go byte-**ordinal** `<`. The standard-parent half
  is unaffected (4-digit ids sort identically). For custom categories whose names
  differ by case/punctuation the document order can differ (a substantial
  minority of the defs that declare custom categories). Only the
  ORDER of top-level `<category>` nodes changes; ids, names, membership and subcats
  are identical, and *arr keys on id, not order. **`[Accepted]`**
- **Same-name custom tie-break** — two customs with identical names tie on the
  sort key; harbrr breaks the tie by ascending id, Jackett by tree-insertion
  order. Sibling order only. **`[Accepted]`**

### Results feed

- **`<jackettindexer>` element name** — kept verbatim for compatibility with
  Torznab consumers that historically scraped Jackett feeds; populated with
  harbrr's indexer id/name. Informational; *arr ignores it. **`[Deliberate]`**
- **`downloadvolumefactor`/`uploadvolumefactor` always emitted** — harbrr's
  normalizer always carries these (defaulting to 1.0), so they are always emitted;
  Jackett omits them when the definition does not extract them. Newznab consumers
  treat an absent factor as 1.0, so an explicit `1` is equivalent. **`[Deliberate]`**
- **`seeders`/`peers` always emitted** — required, non-nullable in harbrr's
  release; Jackett also emits them whenever extracted. **`[Deliberate]`**
- **`files`/`grabs`/`year`/`minimumratio`/`minimumseedtime` omitted at 0** —
  harbrr's non-nullable model cannot distinguish a field that was extracted as 0
  from one that was never present, so 0 is treated as absent and omitted; Jackett
  emits `0` for a present-but-empty non-optional field. A `0` value carries no
  signal a consumer acts on. **`[Accepted]`**
- **Future `pubDate` always clamped to now** — harbrr clamps a future publish date
  to now (`FixResults`); Jackett does this only in release (non-DEBUG) builds.
  harbrr always clamps, matching release-build Jackett. **`[Deliberate]`**
- **`pubDate` timezone** — RFC1123Z preserves the source offset; Jackett renders
  in the host's local offset. Same instant, both valid. **`[Accepted]`**
- **`genre` wire join** — emitted as `", "` (comma+space), matching
  `ResultPage`'s `string.Join(", ", Genres)`; harbrr's internal normalized form
  stays comma-joined (Jackett's filter-facing form). Not a divergence — recorded
  so the two joins are not confused.
- **`language`/`subs` torznab:attrs never emitted** — harbrr's release has no
  language/subs fields, so these attrs are always absent (Jackett omits them when
  null too). **`[Accepted]`**
- **`<newznab:response>` paging element omitted** — harbrr does not declare the
  newznab namespace or emit a `<newznab:response offset="" total="">` element on
  the feed; Jackett's `ResultPage` omits it too, and Sonarr/Radarr treat it as
  optional, so the feed is accepted without it. **`[Deliberate]`**
- **`U+FFFD` handling** — `sanitizeXMLText` strips the Jackett control/BOM/
  noncharacter set and lone surrogates / invalid UTF-8 bytes, but preserves a
  genuine 3-byte `U+FFFD` (which Jackett's regex also preserves). **`[Accepted]`**
- **Download links served direct (direct-link trackers)** — harbrr serves a
  direct-link tracker's download/magnet link as extracted (the passkey it may carry
  is intended output, never logged). These report `NeedsResolver()==false`, so their
  link is served unchanged and a grab works — proven by the live smoke. **`[Accepted]`**
- **Resolver-needing links routed through the /dl proxy** — Resolved. A
  `NeedsResolver()` indexer no longer resolves links inline at feed time (which
  fetched a tracker page per served release). The feed instead emits an opaque
  `/dl?apikey=…&token=…` URL (the pre-resolution link sealed with the keyring, bound
  to the indexer via AAD) and a stable, passkey-free sha256 `<guid>`; all resolution
  + fetching happens once, at grab time, when *arr GETs `/dl`. The `torznab.Indexer`
  contract swaps `ResolveDownload` for `Grab` (resolve + fetch the torrent through
  the session, honouring `download.method`/`headers`; a magnet 302s). The passkey
  never appears in the feed, a log, an error, or a redirect Location. In plaintext
  mode the token degrades to base64url(link) under the loud startup warning; the
  forgeable-token SSRF is a known, gated limitation (apikey-required, encrypted by
  default, single-user self-hosted — no host filter so a LAN tracker still works).
  Tests: `handler_test.go` (`TestServeDL_*`, `TestHandlerProxiesResolverLinks`,
  `TestHandlerProxyGUIDStable`), `dltoken_test.go`. **`[Resolved]`**

### HTTP handler (`internal/web/torznab`)

- **Error-code + HTTP-status policy** — harbrr returns the published
  Newznab/Torznab codes: 100 (HTTP 200) bad apikey, 201 (HTTP 200) unknown
  indexer, 202 (HTTP 400) unknown `t`, 203 (HTTP 400) unadvertised mode or an
  unsupported id param, 900 (HTTP 500) internal error. Jackett funnels unknown-`t`
  and unadvertised-mode through its `CanHandleQuery=false` path to code 201 at
  HTTP 200, and uses HTTP 400 for 900. Sonarr/Radarr key off the `<error code>` in
  the body (codes ≥200 collapse to one exception) and ignore the HTTP status for
  an XML error body, so the two are *arr-equivalent; harbrr keeps the
  spec-accurate codes. **`[Deliberate]`**
- **`atom:link` self URL** — built from the request scheme/host/path with the
  query string dropped (so the apikey is never reflected) and routed through
  `RedactURL`; Jackett uses the bare configured server base URL. Equivalent for
  *arr (the self link is informational). **`[Deliberate]`**
- **id-param gating** — matches Jackett's `ResultsController`: `imdbid`/`tmdbid`
  are rejected (203) only for movie/tv search when the mode does not advertise
  them; `tvdbid` is never param-gated (Jackett gates it only on tv-search
  availability), and general/music/book search never gate an id param — the param
  is accepted and the search degrades to keywords. Parity-positive, recorded so
  the gate scope is explicit.
- **`genre` / `publisher` search params** — `search.Query` has no `Genre` or
  `Publisher` field, so a `genre=`/`publisher=` request param is not threaded into
  the engine's template namespace (a def whose request template reads them renders
  them empty). No vendored fixture relies on this. **`[Accepted]`**
- **`limit`/`offset`** — applied at serialize time over the engine's full result
  set: `limit` is clamped to `[1, 100]`; a non-zero `offset` slices the page,
  whereas Jackett returns an empty set for `offset > 0` on a non-paginating
  Cardigann indexer. De-duplication runs before the limit slice (Jackett limits
  then de-dups), so counts can differ on a duplicate-heavy page. **`[Deliberate]`**
- **Result-category filtering / default categories** — Resolved
  (`internal/web/torznab/filter.go` + `query.go`). The handler now reproduces
  Jackett's two-part behaviour: request-side, `buildQuery` resolves the requested
  newznab cats to tracker cats and falls back to the def's `default: true`
  categories when that resolves to nothing (`CardigannIndexer`:
  `if mappedCategories.Count == 0 -> DefaultCategories`); response-side,
  `filterResults` reproduces `BaseIndexer.FilterResults` — when the request
  supplied categories, a release is kept only if it has no categories or its
  categories intersect the expanded requested set. Note Jackett does NOT return a
  forced empty feed when a `cat` maps to nothing; it searches (defaults or all)
  and the response filter drops non-matches, so an empty feed emerges naturally.
  **`[Resolved]`**
