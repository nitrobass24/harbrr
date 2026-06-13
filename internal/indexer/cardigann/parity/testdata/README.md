# Parity fixtures

Each subdirectory here is one parity **case**: a `case.yml` spec plus the files it
references. The harness (`../parity.go`, driven by `../parity_test.go`) runs the
real Cardigann engine over the saved bytes — offline, no network — and
byte-compares the canonical JSON it produces against the case's golden.

## Case layout

```
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
This is an offline-gate divergence; lazy login is a Phase 4 item.

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
`jackett-port` golden — that would let the engine grade its own homework.

## Regenerating goldens

```
go test ./internal/indexer/cardigann/parity/ -run TestParity -update
```

Only after confirming the output matches the case's oracle.
