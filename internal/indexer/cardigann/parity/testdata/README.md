# Parity fixtures

Each subdirectory here is one parity **case**: a `case.yml` spec plus the files it
references. The harness (`../parity.go`, driven by `../parity_test.go`) runs the
real Cardigann engine over the saved bytes — offline, no network — and
byte-compares the canonical JSON it produces against the case's golden.

## Case layout

```text
<case-name>/
  case.yml        # the spec (see fields below)
  definition.yml  # the tracker definition (or use vendor_def to load a vendored one)
  response.html   # a saved response body (parse mode)
  golden.json     # the expected canonical output
```

## `case.yml` fields

- `name` — label (defaults to the directory name)
- `archetype` — the compatibility-matrix row(s) this case covers (required; the
  success-criteria gate asserts every archetype is exercised)
- `golden_source` — provenance of the golden:
  - `jackett-port` — the expected values are Jackett's own test assertions,
    ported verbatim (the authoritative offline oracle)
  - `hand-derived` — values computed by hand from documented Jackett semantics;
    record the derivation reasoning in `description`
- `mode` — `parse` (extract from a saved body; default) or `search` (drive the
  full login + request-building + parse path against a replay transport)
- `definition` / `vendor_def` — set exactly one
- `response` — saved body file (parse mode)
- `steps` — ordered HTTP exchange (search mode): each step's `method` + `url` is
  asserted (request-construction parity) and its `response` body served with
  `status` (default 200). Include any login probe/request the def implies, in
  order — harbrr logs in eagerly (see "Eager login" below).
- `response_type` — override the def's response type (`json` / empty)
- `base_url`, `clock` (RFC3339), `config` (the `.Config` namespace), `query`
- `golden` — golden filename (defaults to `golden.json`)

## Search mode (request-construction parity)

In `search` mode the replay transport is wrapped in a real `*http.Client` with a
cookie jar, so the production login→search cookie flow is exercised offline. The
transport asserts the engine issued **exactly** the declared `steps` (method +
full URL, in order) and fails loud on any unexpected, mismatched, or unconsumed
step — so a search case pins request construction, not just response parsing.

### Eager login (a documented divergence)

harbrr's `EnsureLoggedIn` runs before every search; for a def with a login block
but no `login.test` block it performs the full login sequence (Jackett instead
logs in lazily, only when a search response looks like a login page). So a
search case for such a def must declare the login request(s) as leading steps.
This is an offline-gate divergence; lazy login is a Phase 5 item.

## Date canonicalization

harbrr emits `publishDate` in its canonical RFC3339 form, whereas Jackett's
`ReleaseInfo.PublishDate` is a `DateTime` it renders as RFC1123Z. Goldens
therefore hold a *translation* of Jackett's value into harbrr's canonical
schema, not Jackett's literal bytes. When porting a Jackett date assertion,
match the **instant** (year/UTC time), never a formatted string, so the
canonical-form choice can never mask an off-by-timezone parse.

## Oracle policy (offline)

Goldens are **not** captured from a live Jackett (project decision; harbrr is
GPL-2.0, same as Jackett, so porting Jackett's own test material is
license-compatible). They come from Jackett's asserted values (`jackett-port`)
or a written hand-derivation (`hand-derived`). Never blindly `-update` a
`jackett-port` golden — the harness refuses it.

The two `jackett-port` oracle cases byte-compare their **whole** `golden.json`,
but only `releases[0]` (and the release count) is anchored to Jackett's own
assertions in `jackett_oracle_test.go`. Releases `[1..N]` of those goldens are a
harbrr regression snapshot, not a Jackett oracle — the `jackett-port` label
covers the count + first release; the remainder is a lock against accidental
change.

## Known divergences from Jackett

These are deliberate or accepted differences from Jackett's Cardigann engine,
documented so a passing gate is honest about what it does and does not match.
None is exercised (and thus hidden) by a fixture authored to dodge it.

**Scope:** this section covers the **engine** layer (a saved tracker response →
normalized release). Output-side differences (the served Torznab/Newznab XML +
the *arr HTTP handler) live in
[`internal/torznab/testdata/README.md`](../../../../torznab/testdata/README.md).
[`docs/divergences.md`](../../../../../docs/divergences.md) is the single index of
both and the shared disposition rule.

Every entry carries an explicit **disposition** so the list is a complete
decision record, not a half-tracked backlog:

- **`[Tracked: Phase N]`** — a real gap with a `docs/plan.md` follow-up item.
- **`[Deliberate]`** — an intentional design choice; not a gap.
- **`[Accepted]`** — a difference we choose to keep (harbrr-additive or
  clean-degradation); no work planned. Revisit only if a vendored def needs it.

Entries:

- **Eager login** — harbrr logs in before the first search (once per Engine);
  Jackett logs in lazily on a logged-out response. See "Eager login" above. A
  login-bearing search case declares the login request(s) as leading steps.
  **`[Tracked: Phase 5 — lazy login]`**
- **Date canonical form** — RFC3339 vs Jackett's RFC1123Z; see "Date
  canonicalization". Same instant, different string — a canonical-schema choice,
  not a parse difference. **`[Deliberate]`**
- **URL encoding of `*()'!`** — both the GET-query encoder (`encodeOrdered`) and
  the search-path value encoder use Go's `url.QueryEscape`, which percent-escapes
  the sub-delimiters `* ( ) ' !` that .NET's `WebUtility.UrlEncode` leaves
  literal. Spaces match (`%20` in the path, `+` in the query). So a keyword
  containing those punctuation characters yields a different — but equivalent —
  request URL. **`[Tracked: Phase 5 — .NET-compatible encoder]`**
- **`.Today.Month` / `.Today.Day`** — harbrr exposes these template fields;
  Jackett seeds only `.Today.Year`. A def referencing them gets a real value in
  harbrr and `""` in Jackett. No vendored def uses them, and the extra fields are
  additive. **`[Accepted: harbrr-additive, no action]`**
- **`leechers` field** — harbrr's canonical release includes `leechers`; Jackett's
  `ReleaseInfo` tracks only `Peers` (= seeders + leechers). A harbrr convenience
  field (useful for downstream Torznab output) with no Jackett equivalent.
  **`[Accepted: convenience field, no action]`**
- **Category ordering** — harbrr sorts a release's categories ascending (a
  deliberate determinism choice for stable goldens); Jackett's `Category` is a
  list in insertion order. They agree whenever insertion order is already
  ascending (as in the JSON oracle, `[2000, 100001]`); a mapping that inserted a
  custom cat before a standard one would differ in order only.
  **`[Accepted: determinism choice, no action]`**
- **`rows.attribute` missing without `MissingAttributeEqualsNoResults`** — when a
  JSON row lacks the `rows.attribute` sub-object, harbrr skips that row; Jackett
  dereferences null and aborts the whole query unless the flag is set. harbrr
  degrades cleanly in both cases (only `yts.yml` pairs the two, with the flag on),
  consistent with the project's clean-degradation stance.
  **`[Accepted: clean degradation, no action]`**
- **Download resolver scope** — `ResolveDownload` covers `before.path/method` +
  selector `selector/attribute/filters/usebeforeresponse`. Out of scope (a def
  using these silently misbehaves rather than erroring): the `.DownloadUri`
  template namespace, `before.inputs`/`before.pathselector`, Go-template
  evaluation of the download selector string, `download.infohash`,
  `download.method: post`, `download.headers`, and `testlinktorrent`.
  **`[Tracked: Phase 7 — complete the download resolver]`**
- **XML backend** — harbrr parses `response.type: xml` into an element tree and
  queries it with cascadia; Jackett uses AngleSharp's `XmlParser`. The common
  RSS/Newznab shapes (`<item>`, `<title>`, `<link>`, `torznab:attr`) match;
  exotic XML (CDATA edge cases, mixed namespaces) is best-effort.
  **`[Tracked: Phase 7 — XML backend edge parity]`**

## Regenerating goldens

```bash
go test ./internal/indexer/cardigann/parity/ -run TestParity -update
```

Only after confirming the output matches the case's oracle.
