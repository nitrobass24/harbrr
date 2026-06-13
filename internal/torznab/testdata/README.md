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

## Known divergences from Jackett / the spec

See the dedicated section added with the results serializer and handler. Every
entry there carries an explicit disposition (`[Tracked: Phase N]` / `[Deliberate]`
/ `[Accepted]`), mirroring the parity fixtures' README.
