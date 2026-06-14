# Phase 5 implementation prompt — Live smoke (closes the MVP)

Paste the block below into an `ultracode` session to implement Phase 5. It begins in **plan mode**:
the agent must first prompt you for live credentials, then plan the *entire* work stream and get the
plan approved before writing any code.

---

ultracode — Implement **Phase 5 (Live smoke — closes the MVP)** from `docs/plan.md` as ONE reviewable
PR. **Begin in PLAN MODE — do STEP 0 before anything else.**

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, edit files, or send any live
request that *mutates* state until the plan is approved. Two things happen in plan mode, in order.

### 0a — Prompt me for credentials + the test bed (do this FIRST)

Phase 5 is the first **LIVE** phase. Before planning, prompt me (use AskUserQuestion / direct
questions) for, and securely intake:

- **Sonarr and/or Radarr** — base URL(s) + API key(s) for the local instance(s) the smoke test drives.
- **Prowlarr** — base URL + API key. Prowlarr is the live **differential oracle**.
- **Which trackers I have working accounts on** (names only). **You select the 5** for the smoke test
  from this set, restricted to **non-Cloudflare** sites (the test env runs **no FlareSolverr/proxy**).
  Propose the 5, state why each is non-CF (evidence from the def — no `cloudflare`/anti-bot markers),
  and let me **confirm** before you request their credentials. Name a fallback or two in case one is
  unhealthy on the day.
- **For the confirmed 5** — the per-def credential fields each needs (passkey / cookie / login
  user+pass / apikey).
- **qBittorrent + qui** — already in the LAN; confirm the *arr's download client is wired (the grab
  half needs a release to actually reach qBittorrent).

**Credential-handling rules (NON-NEGOTIABLE — AGENTS.md):**
- **NEVER** write any credential or API key to a repo file, the plan, a fixture, `testdata/**`, a
  commit, or a log. They live ONLY in harbrr's encrypted store (the gitignored data dir) and in
  working memory for this run.
- Never echo a credential back in plaintext — refer to them by tracker/field name. Redact everywhere.
- At execution time, enter tracker creds into the **running daemon via the management API**
  (`POST /api/indexers`) so they land **AES-256-GCM-encrypted**; never hand-write them into the DB or
  a file.
- Any fixture captured from a live response is **secret-scrubbed** (passkeys/cookies/tokens redacted)
  **before** it is committed.
- You MAY do **read-only** connectivity checks in plan mode (e.g. *arr `GET /api/v3/system/status`, a
  Prowlarr ping) to confirm the keys work and inform tracker selection — read-only, no writes, no
  indexer adds.

### 0b — Produce ONE complete plan for the entire Phase 5 work stream

Plan all **eight** work-list items end to end (not just the first). Pressure-test the plan with a
validator/architect agent (and revise), then present it with ExitPlanMode for my approval and wait.

The plan must cover:
- **Tracker selection** — the 5 non-CF trackers + justification (non-CF evidence per def; account
  availability), and the fallback set.
- **The LIVE vs OFFLINE split per item** — which items are gated by committed **offline deterministic
  tests** (serializer fuzz, .NET URL encoder, lazy-login logic, category filtering, Test action) and
  which by the **live smoke + Prowlarr differential** (the 5-tracker run + grab end-to-end).
- **The differential-oracle method** — same query → Prowlarr feed vs harbrr feed → **tolerance-based**
  diff (live data is non-deterministic: compare title/size/category/count within defined bounds, not
  byte-exact). Define what counts as a pass.
- **The download path for grab** — confirm the 5 trackers need only the **basic** resolved/proxied
  download link (Phase 2 ships selectors + `before.path`; the full resolver is Phase 7). If a selected
  tracker needs the Phase 7 resolver, swap it or scope the `/dl` proxy to the basic case and record the
  gap as `[Tracked: Phase 7]`.
- **Package/file layout** — every file to create/modify: the .NET URL encoder in the query/path value
  encoders; lazy-login in the login/search stage; category filter + default-cat substitution in
  `internal/torznab`; the serializer fuzz/property test; the resolved-download wiring (inline vs a
  `/dl` proxy endpoint); the indexer **Test** action handler + `login.test` wiring; the live-smoke
  harness (**build-tagged**, env-var creds, never in normal CI).
- **Architecture decisions to confirm** — where lazy-login replaces eager once-per-Engine login
  (Phase 2) without breaking the parity goldens; whether the resolved link is served inline or via a
  `/dl` proxy (and that endpoint's **auth + SSRF** posture); how the live harness is gated.
- **Test strategy per item → oracle mapping**; **commit/box sequencing** (which `docs/plan.md` box each
  commit ticks — **including the leftover Phase 3 box** "Sonarr/Radarr can search a handful of real
  trackers… end-to-end").
- **Risks + mitigations** — account bans / rate limits, a CF surprise on a "non-CF" tracker, credential
  leak via the `/dl` proxy or logs, the non-deterministic differential, and the Phase-7 resolver
  dependency.

After I approve via ExitPlanMode, leave plan mode and execute the PER-ITEM LOOP below.

## READ FIRST

`AGENTS.md`; `docs/plan.md` (Phase 5 **+ the execution-protocol blockquote**); `docs/ideas.md` **§9
(Security model)** + the fetch/auth + download sections; `docs/architecture.md` — **invariant #3** (the
Torznab *arr-facing contract and the management OpenAPI surface stay SEPARATE) and **invariant #5**
(SQLite only). `docs/divergences.md` is the divergence-ledger index. Read the divergence notes this
work builds on: `internal/indexer/cardigann/parity/testdata/README.md` ("Eager login"; "Known
divergences" for the URL encoder) and `internal/torznab/testdata/README.md` (category filtering +
download-link notes).

Phase 5 proves the daemon **LIVE**: a real Sonarr/Radarr drives harbrr against 5 real non-Cloudflare
trackers, **search → grab end-to-end**, with Prowlarr as a differential oracle. It **closes the MVP
(Phases 1–5)**. It also lands the offline-testable behaviours live *arr search needs: lazy login, the
.NET URL encoder, result-category filtering + default categories, the served/proxied download link, the
indexer Test action, and the serializer robustness proof.

## CONTEXT (Phase 4 shipped — the merged daemon foundation)

- `harbrr serve` now boots a real daemon: SQLite + migrations, the §9 secrets store (AES-256-GCM
  tracker creds, argon2id password, SHA-256 API keys, auto-keyfile, fail-loud canary), first-run setup
  + login + `X-API-Key`, the indexer-instance registry as the production `torznab.Provider`, the
  management API (`/api/indexers`, `/api/apikeys`, `/api/auth/*`), and the Torznab handler mounted at
  `/api/v2.0/indexers/...`.
- Engine: `cardigann.NewEngine(def, WithDoer/WithConfig/WithClock/WithBaseURL)`, `Capabilities()`,
  `Search(Query)`, `ParseResponseQuery`, `ResolveDownload`. The login/session executor exists
  (`form`/`post`/`get`/`cookie`, CSRF, cookie jar, re-login) but logs in **EAGERLY** once per Engine
  (Phase 2) — Phase 5 makes it **lazy**.
- `registry.WithDoerFactory` seam exists (per-instance HTTP client) — relevant to the download proxy +
  the Phase 6 per-indexer proxies.
- Phase 2 leaves `url.QueryEscape` in the query/path encoders (`*()'!` escaped differently from .NET)
  and ships selectors + `before.path` for downloads (the full resolver is Phase 7).

## HARD RULES (do not work around)

- **LIVE credentials** — per STEP 0: never logged/committed/echoed; encrypted store only; redacted
  everywhere; any captured fixture is **secret-scrubbed** before commit.
- **LIVE traffic discipline** — gentle rate: **sequential** queries, low concurrency, sane delays,
  respect each def's rate limits; **non-Cloudflare trackers ONLY** (no FlareSolverr in the env). If a
  tracker returns rate-limit / anti-bot / CF, **back off and report** — do NOT hammer or risk a ban.
- **The live smoke + differential are an integration gate** — run manually / under a **build tag** with
  **env-var** creds. They must **NEVER** run in normal CI and must **never** require committed secrets.
  CI stays fully **offline and deterministic**.
- **SQLite only**; pure-Go driver; **two HTTP contracts stay separate** (invariant #3); OpenAPI changes
  → `make test-openapi`. Carry **every** Phase 4 hard rule forward.
- NO AI attribution/co-author/"Generated with" lines. Conventional commits; gofumpt-clean; interfaces
  ≤5 methods; no `map[string]any` for structured data; split god-functions
  (funlen/gocyclo/gocognit/nestif). Before EVERY commit: `make precommit` + `make build` green; tests
  always `-race -count=1`.
- Branch off main: `phase5/live-smoke`. NEVER touch main (protected; required checks: `test`, `build`,
  the five `cross-build (...)`, `secret scan`; lint + CodeQL also run). One `docs/plan.md` item per
  commit; tick its box in the SAME commit, only when its tests are green.

## ORACLE / FIXTURES (decided): LIVE smoke + Prowlarr differential, with OFFLINE deterministic gates

- **Live smoke** (the MVP gate; manual / build-tagged): a real Sonarr/Radarr parses harbrr's caps and
  completes **search → grab end-to-end** against the 5 live trackers (a release reaches qBittorrent and
  the `.torrent`/magnet resolves) — not just a 200 feed.
- **Differential oracle** (Prowlarr): same query to Prowlarr's Torznab feed and harbrr's feed for the
  same tracker; diff normalized results within a **defined tolerance** (non-deterministic live data —
  compare title/size/category/count within bounds, not byte-exact). Record the method + tolerance.
- **Offline deterministic** (committed; runs in CI): serializer **fuzz/property** test (arbitrary
  `[]*Release` → well-formed, namespace-bindable XML, never panics); **.NET URL-encoder KAT** (`*()'!`
  + unicode vs `WebUtility.UrlEncode` expected); **lazy-login** unit test (logged-out detection →
  relogin → **retry-once** bound, over a replay `Doer`); **category-filter** unit test (drop
  non-matching cats, empty feed on no-match, default-cat substitution, over saved fixtures); **indexer
  Test-action** unit test (`login.test` probe over a replay `Doer`).
- **Live evidence** is captured in the PR body / a Phase 5 testdata README (counts, pass/fail per
  tracker, the grab proof, the Prowlarr diff) — **NOT** committed creds, NOT raw unscrubbed live
  responses.

## WORK LIST — each unchecked Phase 5 box is one item

1. **5 real non-Cloudflare trackers** (agent-selected; no FlareSolverr), live login/session, gentle
   rate — the live smoke run.
2. **Robustness proof** — a real Sonarr/Radarr completes **search → grab end-to-end** against the live
   trackers, **plus** an offline serializer fuzz/property test (arbitrary `[]*Release` → well-formed,
   namespace-bindable XML, never panic). (Also closes the leftover Phase 3 box.)
3. **Lazy login** — log in only when a search response looks logged-out, then retry once — replacing
   the eager once-per-Engine login.
4. **.NET-compatible URL encoder** — replace `url.QueryEscape` in the query/path encoders so `*()'!`
   match `WebUtility.UrlEncode`.
5. **Fetch/auth matrix rows as available** — Cloudflare/FlareSolverr (pluggable **solver seam**) ·
   2FA/manual-cookie. Build the seam; rows that need infra absent from the env are scoped/deferred with
   a disposition.
6. **Result-category filtering + default categories** — drop result rows whose cats miss the query cats
   (Jackett `FilterResults`); return an empty feed when every requested `cat` maps to no tracker cat;
   substitute a def's `default: true` cats when the mapped tracker-cat list is empty.
7. **Serve resolved/proxied download links** — wire `ResolveDownload` into the served feed (optionally
   a `/dl` proxy) so a grab downloads through harbrr's session, not the raw tracker link. Basic case
   now; the full resolver depends on Phase 7 — scope + record any gap.
8. **Indexer "Test" action** — validate a configured indexer's creds/connectivity before saving, via
   the management API (the engine's `login.test` probe wired to a persisted instance).

## SUCCESS CRITERIA — assert as a gate

- A real Sonarr/Radarr searches **5 live non-CF trackers** through harbrr and completes a **grab
  end-to-end** (release → qBittorrent), gently rate-limited, **no bans**.
- harbrr's feed matches **Prowlarr's** within the defined tolerance for the same query on the same
  trackers.
- Lazy login, the .NET URL encoder, category filtering + default cats, the served/proxied download
  link, and the Test action all pass their **offline deterministic** tests; the serializer **never
  panics** on fuzzed release shapes.
- **No credential** ever appears in a log, error, the served feed, a fixture, or a commit; redaction
  holds end-to-end; the live harness needs **no committed secrets** and never runs in normal CI.
- `make precommit` + `make build` green; all 5 cross-builds green; contracts still separate; SQLite-only.

## PER-ITEM LOOP (after plan approval; one commit per item)

(a) brief per-item plan consistent with the approved master plan; (b) IMPLEMENT + table-driven tests
beside it (offline/deterministic where the behaviour allows; the live items carry their build-tagged
harness + captured evidence); (c) VERIFY `make precommit` + `make build`, `-race`; (d) ADVERSARIAL
REVIEW — ≥3 independent skeptics try to REFUTE it (lazy-login false-positive / relogin loop / retry
bound; URL-encoder parity incl. unicode; category-filter empty-feed + default-cat off-by-one;
serializer panic/escape/namespace; download-proxy **credential leak / SSRF / auth**; any live-cred
leak path; live-rate discipline). Fix every confirmed issue; re-verify. (If skeptic agents die on a
spend limit, fall back to rigorous inline self-review and SAY SO.) (e) COMMIT: one focused conventional
commit; tick the box in the same commit.

## AFTER ALL ITEMS

- f) END-TO-END PHASE REVIEW + completeness critic ("which live tracker / cred path / serializer edge /
  differential claim is unverified?"); close gaps. Capture the live-smoke evidence (per-tracker
  pass/fail, counts, grab proof, Prowlarr diff) in a Phase 5 testdata README. Record any divergence
  (lazy-login vs the Phase 2 eager login now reconciled; the URL-encoder change; any deferred fetch/auth
  row or Phase-7 resolver gap) with an explicit disposition and add it to `docs/divergences.md`. Add the
  Phase 5 improvements to `docs/highlights.md` (honestly labeled `[shipped]`/`[partial]`/`[planned]`).
- g) KEEP THE PR ≤150 FILES (CodeRabbit auto-skips above 150; split a self-contained chunk into a
  second PR + note merge order if needed). Don't open multiple PRs + force-push in rapid succession
  (CodeRabbit ~1h rate-limit; it auto-reviews on PR-open, so do NOT post `@coderabbitai review`
  redundantly).
- h) OPEN ONE PR: `phase5/live-smoke → main`, with a summary + testing checklist + a coverage table
  (lazy login, URL encoder, category filter, download link, Test action, serializer fuzz, live smoke,
  Prowlarr differential). No AI attribution. **No creds in the PR body.**
- i) CI GREEN: push, fix until all required checks pass (test, build, cross-build ×5, secret scan). CI
  is fully offline — the live harness does not run here.
- j) CODE REVIEW: let CodeRabbit's auto-review complete; address EACH finding (validate → fix +
  revalidate, or reply inline why it's skipped/intentional). Re-run CI.
- k) PAUSE: once CI + review are green, STOP. Do NOT merge. Wait for my review.

## FINAL REPORT

Items shipped (commit ids); the 5 trackers tested + per-tracker live result (search + grab); the
Prowlarr differential result + tolerance; offline test coverage (lazy login, URL encoder, category
filter, download link, Test action, serializer fuzz); cross-build status; explicit confirmation that no
credential was logged or committed; known divergences + dispositions (incl. any deferred fetch/auth row
or Phase-7 resolver gap); and open questions.
