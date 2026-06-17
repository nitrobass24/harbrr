# Phase 7 implementation prompt — Complete the engine

Paste the block below into an `ultracode` session to implement **Phase 7 (Complete the engine)** of
harbrr — the last Cardigann-parity work: finish the **download path** and close the remaining
**selector/XML edge gaps** so harbrr matches Jackett on *every* tracker shape, not just direct-link
ones. This is the deliberate, **scoped un-freeze** of the engine (Phase 6 froze it for operational
safety); it stays **offline-gated against the parity oracle** — every change keeps the existing parity
suite green and adds new fixtures.

It is split into **two PRs** off fresh `main`:

- **PR #1 — `phase7/download-resolver`** — complete `ResolveDownload` (the six Cardigann download
  features) + the grab-time **`/dl` proxy** (the output-layer half of the same feature).
- **PR #2 — `phase7/xml-coverage`** — XML backend edge parity (CDATA / mixed namespaces /
  AngleSharp-vs-cascadia) + broadened response-mode / selector / date fixtures.

This split is deliberate: the download path is one cohesive, high-blast-radius change (loader fields +
template namespace + resolver + a new HTTP endpoint + fixtures), while the XML/coverage work is
independent engine-edge parity. Splitting keeps each PR reviewable and under CodeRabbit's **150-file
cap** (it silently auto-skips any PR over 150 files). PR #1 merges before PR #2 opens.

---

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, edit files, or send any live
request that *mutates* state until the plan is approved. Two things happen in order:

### 0a — Prompt me for the (optional) live resource + the test bed (do this FIRST)

Phase 7 is **offline-gated**: every box closes on committed deterministic fixtures regardless of any
live resource. The live confirmation — a real grab through a **resolver-needing tracker** (a def with a
`download` block / `before` / `infohash`, not a direct-link tracker) — is the **Phase 9** gate, not
this phase. So ask me, but default to deferring:

- **A resolver-needing tracker** (creds entered into the running daemon's encrypted store via the API,
  never chat/repo) for an *optional* end-to-end `/dl` grab confirmation. If I can't supply one on the
  day, record it `[Tracked: Phase 9 — live validation]` — do **not** fake it and do **not** manufacture
  an empty commit. Offline fixtures (stub download pages) are the gate either way.
- The **Phase-5 test bed** (Prowlarr for the differential, qBittorrent for a real grab) only if we do
  the optional live confirmation.

Record each resource I cannot supply as **DEFERRABLE-with-disposition** (`[Tracked: Phase 9 — …]`)
rather than blocking. The engine code is fully offline-testable; live is confirmation, not the gate.

### 0b — Produce ONE complete plan for the entire Phase 7 work stream (both PRs)

Cover **both PRs**, not just PR #1. Budget each PR independently against the 150-file cap (fixtures can
balloon — say which fixtures land where) and state the merge order (PR #1 before PR #2 opens). Pressure-
test the DECIDED architecture below against the current code with a seam-verification pass (the seams
moved since these line numbers were written — trust the named symbol and re-locate it). Present the plan
with `ExitPlanMode` and wait for approval before leaving plan mode.

---

## READ FIRST

- `AGENTS.md` (the prime directive + non-negotiables), `docs/plan.md` (Phase 7 boxes), `docs/ideas.md`
  (the full design — download resolver, response modes), `docs/architecture.md` (the engine pipeline
  shape — read before touching cross-stage data flow), `docs/divergences.md` (an **INDEX**, not a
  free-text append target).
- **Seam citations are by symbol + approximate line.** If a cited line has moved, **trust the named
  symbol/function and re-locate it** — do not treat a stale line number as evidence the site is gone.
- The download path today (PR #1 territory):
  - `search.ResolveDownload` (`internal/indexer/cardigann/search/download.go:~29`) — currently handles
    `before.path` + `before.method` (GET/POST) + a download `selector`/`attribute`/`filters`/
    `usebeforeresponse`; returns the link unchanged when there is no `download` block. The comment block
    at the top already enumerates the Phase-7 gaps.
  - Engine wiring: `Engine.ResolveDownload` (`engine.go:~316`), consumed by the Torznab handler
    `resolveDownloadLinks` (`internal/web/torznab/handler.go:~131`); the `/dl` proxy is explicitly
    deferred at `handler.go:~137`.
  - The `.DownloadUri` template namespace **context already exists** (`template/context.go:~79`) but is
    **not yet referenced** by template evaluation (`template/template.go`).
  - Loader model (`internal/indexer/cardigann/loader/model.go`): `testlinktorrent` is parsed (`:~24`)
    but unused; `before.pathselector` (`:~372`), `download.method` (`:~362`), `download.headers`
    (`:~366`) fields are absent or omitted; `before.inputs` and `download.infohash` are absent.
- The XML/selector path (PR #2 territory):
  - `selector.ParseXML` → `xmlToNode` (`internal/indexer/cardigann/selector/xml.go:~26,39`) builds an
    `html.Node` tree (per-element xmlns scope tracking `:~45`, `qualifyName` `:~118`, `elementAttrs`
    `:~102`), fed to cascadia. CDATA currently round-trips as `CharData` text. Response-mode dispatch:
    `search/search.go:~28,130`.
  - The parity gate: `internal/indexer/cardigann/parity` + each stage's fixture suite; the differential
    RE2-vs-regexp2 suite. **These must stay green** — Phase 7 *adds* fixtures, never silences a diff.

---

## CONTEXT (Phase 6 shipped — operationally safe; the engine is otherwise complete)

Phases 1–5 proved the Cardigann engine live (MVP); Phase 6 added operational safety (per-host pacing,
timeouts, 429/503 backoff, per-indexer health/status, proxies, key rotation, the FlareSolverr solver).
The engine matches Jackett on the vendored corpus **except** the download-resolver completeness and a
few XML/selector edge cases — that residue is Phase 7.

**Seams Phase 7 builds on (already in place, do NOT re-invent):**

- The full pipeline `loader → mapper → template → filter → selector → dateparse → regexadapter → login
  → search → normalizer` (`internal/indexer/cardigann/doc.go`). Phase 7 touches **template** (the
  `.DownloadUri` namespace), **selector** (XML edge cases), and the **download** path
  (`search/download.go` + the Torznab output layer).
- `ResolveDownload` already threads the request `ctx`, the session jar, and `before.path`/selector
  evaluation — **extend it, don't greenfield it**.
- The `.DownloadUri` template context is already built — Phase 7 **wires it into template evaluation**,
  it does not invent it.
- The session-aware fetch (login jar + paced doer) used by search is the same client the `/dl` proxy
  resolves through at grab time.
- Secret redaction chokepoints (`internal/http` `RedactURL`/`RedactError`) — the download link carries a
  passkey; reuse them, do not add new logging that bypasses them.

---

## HARD RULES (do not work around)

- **The engine un-freeze is scoped to ADDING parity, never breaking it.** Every change keeps the
  existing parity suite + differential suite **green**; new behavior ships with **new fixtures** derived
  from Jackett's output. Never silence a parity diff by editing a def or loosening an assertion.
- **Never hand-edit vendored definitions** under `internal/indexer/definitions/vendor/`. Absorb every
  behavioral difference in the engine (or `dropin/`). Magnet/URI/CDATA quirks are engine work.
- **Match Jackett byte-for-byte on the resolved artifact.** A synthesized magnet
  (`download.infohash`) must match Jackett's `magnet:?xt=urn:btih:…&dn=…&tr=…` construction; the
  `.DownloadUri` namespace must match .NET `Uri` semantics (`AbsoluteUri`, `Query`, escaping). Pin each
  with a golden fixture.
- **Secret redaction stays absolute.** Download links embed passkeys; the `/dl` proxy resolves them
  server-side. Never log/serve/error a raw passkey-bearing URL — route through the redaction chokepoints.
  The `/dl` proxy's *purpose* is partly that the passkey stays inside harbrr and never reaches the
  feed/*arr; do not regress that.
- **`testlinktorrent` must not fire uncontrolled live requests.** It validates a resolved link; gate it
  so it is offline-testable (stub doer) and bounded — no per-search hammering of a tracker.
- **SQLite only**; pure-Go; the two HTTP contracts stay separate (Torznab feed vs management API —
  the `/dl` proxy lives on the Torznab/serving tree, not the management tree).
- **NO AI attribution/co-author/"Generated with" lines.** Conventional commits; gofumpt-clean;
  interfaces ≤5 methods; no `map[string]any` for structured data; split god-functions (funlen/gocyclo).
- **Branches & box rule.** Each PR off `main` on its named branch; NEVER touch `main` directly.
  Box-bearing items tick their `docs/plan.md` box in the **same commit**, only when its tests are green.
  Enabling-infra (loader-field additions alone, the `.DownloadUri` namespace wiring) ticks **NO box** —
  say so in the commit message; the box closes with the feature it enables.

---

## ORACLE / FIXTURES (decided): OFFLINE + deterministic, live grab gated out of CI

**Offline deterministic (committed; runs in CI — the gate for both PRs):**

- **Download resolver** — table-driven against a **stub HTTP server / replay Doer**: a def `download`
  block + a canned before/download page (HTML or JSON) + headers → assert the resolved link, the
  synthesized magnet (`infohash`), the POST body/headers (`method: post`, `download.headers`), and the
  `before.inputs`→template→`before.pathselector` flow. Golden outputs derived from Jackett.
- **`.DownloadUri` namespace** — fixtures over `.DownloadUri.AbsoluteUri`/`.Query.*`/`.AbsolutePath`
  in selector filters and path expressions, matched against .NET `Uri` semantics.
- **`/dl` proxy** — a handler test: a feed `guid`/token → harbrr resolves through the session → serves
  the `.torrent` (or 302), with the passkey **redacted** from logs and **absent** from the served feed.
- **XML edge parity** — `xml_test.go` fixtures for CDATA boundaries, mixed/redeclared/undeclared
  namespaces, comments, namespaced attributes; assert selector matches vs golden. Existing XML fixtures
  stay green.
- **Coverage broadening** — added parity-corpus + per-stage selector/date fixtures across JSON/HTML/XML
  response modes; the differential RE2-vs-regexp2 suite stays green.

**Operator-resourced LIVE (manual / build-tagged; never in CI; → Phase 9):** a real grab through a
resolver-needing tracker via `/dl` → seeding in qBittorrent (left seeding, no hit-and-run). If supplied
on the day it's a bonus confirmation captured secret-free in the smoke README; otherwise
`[Tracked: Phase 9 — live validation]`.

CI stays fully **offline and deterministic** — no live tracker, no network.

---

## WORK LIST — items in dependency order, mapped to the two PRs

### PR #1 — `phase7/download-resolver`

1. **Loader model: the new download/before fields.** Add `before.inputs` (map), `before.pathselector`
   (SelectorField), `download.infohash` (block), `download.method` (`post`), `download.headers` (map),
   and surface the already-parsed `testlinktorrent`. *(enabling — ticks NO box; the box closes with
   item 3.)*
2. **`.DownloadUri` template namespace wiring.** Make template evaluation reference the existing
   `.DownloadUri` context so selector filters / path expressions can use `.DownloadUri.AbsoluteUri`,
   `.Query.*`, etc., matching .NET `Uri` semantics. *(enabling — ticks NO box.)*
3. **Complete `ResolveDownload`.** `before.inputs`→template context→`before.pathselector`;
   download-selector template eval; `download.infohash`→magnet synthesis (Jackett's magnet format);
   `download.method: post` + `download.headers`; `testlinktorrent` validation (bounded, stub-testable).
   *(plan.md "Complete the download resolver" box.)*
4. **The grab-time `/dl` proxy.** A serving-tree endpoint that takes a feed `guid`/opaque token,
   resolves the real (passkey-bearing) link through harbrr's session, and serves the `.torrent` (or
   302) — so the passkey stays in harbrr and never reaches the feed/*arr. Redaction holds; lives on the
   Torznab/serving tree (invariant #3). *(closes the `[Tracked: Phase 7]` `/dl` ledger item; folds into
   the "Complete the download resolver" box if not already ticked, else ticks NO box — say which.)*

### PR #2 — `phase7/xml-coverage`

5. **XML backend edge parity.** CDATA handling (preserve content/boundaries for selectors),
   mixed/redeclared/undeclared namespaces, comments, namespaced attributes — match AngleSharp where it
   matters; record any deliberate divergence. Existing XML parity stays green.
   *(plan.md "XML backend edge parity" box.)*
6. **Broaden response-mode / selector / date coverage.** Expand the parity corpus + per-stage fixtures
   across JSON/HTML/XML; pin remaining `:contains`/`:has` / date-format edges as fixtures.
   *(plan.md "Broaden response-mode and definition coverage" box.)*

**Explicitly OUT of scope (do NOT build here):** native Avistaz (`[Tracked: Phase 8]`), backup/restore +
the web UI + app-sync + migration import + OIDC + stats (`[Tracked: Phase 10]`), the broad live Prowlarr
differential + live grabs (`[Tracked: Phase 9]`).

---

## RISKS (carry into the plan with concrete tests/mitigations)

- **Magnet-format mismatch** (`infohash`→magnet) vs Jackett — pin the exact `xt=urn:btih:…&dn=…&tr=…`
  construction (hex vs base32 hash, trackers, `dn` escaping) with a golden fixture.
- **`.DownloadUri` .NET-vs-Go URI semantics** — `AbsoluteUri`/`Query` parsing/escaping differ between
  .NET `Uri` and Go `net/url`; fixture each member used by real defs; route exotic cases through the
  existing encode/regex layers.
- **`before.inputs`→`before.pathselector` ordering** — inputs must render into the template context
  *before* the pathselector evaluates; test the composed flow, not just each piece.
- **CDATA/namespace changes regress existing XML parity** — run the full XML + parity suite on every
  change; add new fixtures alongside, never mutate existing golden outputs to fit new code.
- **`/dl` proxy leaking a passkey** in a log/error/redirect, or exposing it in the served feed — assert
  redaction and feed-absence in the handler test.
- **`testlinktorrent` firing uncontrolled live requests** during search — gate it; prove it stub-testable
  and bounded.
- **150-file cap** — fixtures (corpus broadening especially) can balloon PR #2; budget independently and,
  if PR #2 nears 150, split coverage-broadening into a third PR and state the merge order.

---

## SUCCESS CRITERIA — assert as a gate

- `ResolveDownload` handles all six Cardigann download features (`before.inputs`/`before.pathselector`,
  selector template eval, `download.infohash`→magnet, `method: post`, `download.headers`,
  `testlinktorrent`) — proven offline against stub-server fixtures; synthesized magnets and `.DownloadUri`
  members **match Jackett** on golden fixtures.
- The `/dl` proxy resolves a feed link through the session and serves the `.torrent`, with the passkey
  **redacted from logs** and **absent from the served feed** — proven by a handler test.
- XML CDATA + mixed/redeclared namespaces parse and select correctly; the existing XML + parity +
  differential suites stay **green**; new behavior is pinned by new fixtures.
- Coverage broadened across response modes; no parity regression.
- `make precommit` + `make build` green (`-race`); all cross-builds green; contracts still separate;
  SQLite-only; **each PR ≤150 files**.
- The live grab through a resolver-needing tracker is captured secret-free **or** recorded
  `[Tracked: Phase 9 — live validation]`.

---

## PER-ITEM LOOP (after plan approval; one commit per item)

For each WORK LIST item:

- **(a)** brief per-item plan consistent with the approved master plan.
- **(b) IMPLEMENT + table-driven tests beside it**, offline/deterministic (stub server / replay Doer for
  the resolver + `/dl`; golden fixtures for magnet, `.DownloadUri`, CDATA/namespaces; the parity corpus
  for coverage). Derive every golden output from Jackett's behavior.
- **(c) VERIFY** `make precommit` + `make build`, `-race`; parity + differential suites green.
- **(d) ADVERSARIAL REVIEW** — ≥3 independent skeptics try to REFUTE it. Targets to survive: magnet/URI
  byte-mismatch vs Jackett; `before.inputs`/`pathselector` ordering or template-context leakage;
  `download.method: post` body/header correctness; `testlinktorrent` firing live or unbounded; a passkey
  leaking through `/dl` (log/redirect/feed); CDATA/namespace change silently regressing an existing
  fixture; a parity diff silenced by a def edit instead of an engine fix; `/dl` mounted on the wrong HTTP
  tree. Fix every confirmed issue; re-verify. If skeptic agents die on a spend limit, fall back to
  rigorous inline self-review and **say so**.
- **(e) COMMIT** — one focused conventional commit; tick the `docs/plan.md` box in the **same commit**
  only for box-bearing items (state explicitly when an item ticks no box).

---

## AFTER ALL ITEMS (per PR)

- **(f) END-TO-END PHASE REVIEW + completeness critic.** Close gaps. Record every divergence with an
  explicit disposition in the owning layer's testdata README (download resolver / `/dl` →
  `parity/testdata/README.md` + `torznab/testdata/README.md`; XML edges → `parity/testdata/README.md`)
  **and** add ONE row to `docs/divergences.md`'s layer table pointing at it — `docs/divergences.md` is an
  **INDEX**, do NOT free-text entries into it. Flip the now-completed `[Tracked: Phase 7]` entries to
  `[Resolved: Phase 7]`. Add the Phase 7 improvements to `docs/highlights.md` (honestly labelled
  `[shipped]`/`[partial]`).
- **(g) KEEP EACH PR ≤150 FILES** — guaranteed by the two-PR split; if PR #2 still threatens 150, split
  coverage-broadening into a third PR and note merge order.
- **(h) OPEN THE PR** — branch → `main`; summary + testing checklist + coverage table; no AI attribution;
  **no creds / passkeys / tracker URLs in the PR body**.
- **(i) CI GREEN** — push; fix until all required checks pass.
- **(j) CODE REVIEW** — let CodeRabbit auto-review complete (mind the ~1h rate limit; open once, don't
  force-push in rapid succession); address EACH finding (validate → fix + revalidate).
- **(k) PAUSE** — once CI + review are green, STOP; do NOT merge; wait for approval.

---

## FINAL REPORT

State the items shipped (commit ids) per PR; the download features as built vs the boxes; the offline
test coverage by area (resolver fixtures, magnet/`.DownloadUri` goldens, `/dl` handler test, CDATA/
namespace fixtures, broadened corpus, differential green); the live grab result or its
`[Tracked: Phase 9]` disposition; explicit confirmation that no passkey appears in a log, error, the
served feed, or a commit (redaction holds end-to-end through the new `/dl` surface); the known
divergences + dispositions; and any open questions. State which required checks ran, which were
skipped/deferred and why, and any unresolved failures.
