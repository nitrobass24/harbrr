# harbrr build plan

The executable checklist. Work **top to bottom, one item at a time**, and check a box only when its
tests are green (`make precommit` clean). Ordered by **risk retirement**, not product completeness —
the engine must prove it can match Jackett on saved inputs before any product surface is built. Full
rationale in `ideas.md`; rules in `../AGENTS.md`.

Legend: `[ ]` todo · `[x]` done · each leaf should land in its own focused commit.

---

## Phase 0 — Foundations (scaffold; mostly done)

- [x] Repo skeleton, package layout, `AGENTS.md`/`CLAUDE.md`, `.golangci.yml`, Makefile, CI, hooks
- [x] `make tools` runs clean on a fresh checkout
- [x] `make vendor-defs` populates `internal/indexer/definitions/vendor/` (pin `JACKETT_REF` to a SHA)
- [x] `make build` and `make test` green with the vendored snapshot embedded
- [x] Author the management-API `openapi.yaml` stub under `internal/web/swagger` + drift test
      (`make test-openapi`)
- [x] Wire `cobra`/`viper` entrypoint and a typed config struct (no `map[string]any`)

## Phase 1 — Engine proof (offline) — *retires the existential risk*

Build the pipeline stage by stage, each table-driven-tested with its own fixtures. Keep stages
decoupled.

- [x] **loader** — parse + schema-validate a definition into a typed model; precedence dropin > vendor
- [x] **mapper** — capabilities document + category mapping (Newznab category system)
- [x] **template** — Go `text/template` with .NET-equivalent truthiness (empty-vs-missing)
- [x] **filter** — the bounded filter registry; start with the 6 dominant ops (`re_replace`, `replace`,
      `append`, `dateparse`, `regexp`, `querystring`), then the tail
- [x] **selector** — HTML (`cascadia`/`goquery`) + JSON selection; start the standing selector fixture
      suite (vs Jackett semantics)
- [x] **dateparse** — .NET format strings → Go layout; cover timezones, relative dates, localized names
- [x] **regexadapter** — RE2 default; route to `regexp2` on opt-in / non-Latin `language:` / RE2
      compile-failure / .NET-only constructs; run both engines on shared fixtures
- [x] **login/session executor** — `form`/`post`/`get`/`cookie`, CSRF, cookie jar, re-login;
      manual-cookie fallback. Test offline against saved login sequences
- [x] **normalizer** — produce normalized release objects (canonical, deterministic JSON)
- [x] Engine assembles the stages end-to-end on a saved response

## Phase 2 — Offline parity — *the gate*

- [x] Port Jackett's GPL-2.0 Cardigann engine tests (`CardigannIndexerHtmlTests`/`JsonTests`) — fixtures
      byte-for-byte under `parity/testdata/jackett/` (+ NOTICE); `jackett_oracle_test.go` asserts
      Jackett's exact request URLs and first-release values (25 / 78 releases)
- [x] Build the differential harness (offline oracle: goldens ported from Jackett's own test
      assertions or hand-derived from documented Jackett semantics, never captured from a live
      Jackett — see `parity/testdata/README.md`; each case records `golden_source` provenance)
- [x] Wire `internal/indexer/cardigann/parity` to the real engine (replace the stub `Process`)
- [x] Pass the **compatibility matrix** offline rows (each archetype has a fixture):
  - [x] HTML / form login
  - [x] HTML / cookie login
  - [x] JSON-API
  - [x] XML / Newznab
  - [x] non-Latin-script (regexp2 path)
  - [x] freeleech (download/uploadvolumefactor)
  - [x] multi-category
  - [x] date-heavy (multiple .NET formats + relative)
  - [x] magnet-only (magnet/infohash synthesis)
  - [x] download-link pre-request
- [x] **Success criteria met:** 100% defs load w/o panic · zero silent schema failures (triaged to a
      visible skip-list) · ported Jackett tests pass · matches Jackett on ≥25 saved fixtures · secrets
      redacted in logs · broken indexers degrade cleanly

## Phase 3 — Minimal Torznab output

- [ ] `internal/torznab`: capabilities document + `t=caps|search|tvsearch|movie|music|book`
- [x] **caps/category correctness is a gate** (Sonarr/Radarr failures usually trace here)
- [ ] Sonarr/Radarr can search a handful of real trackers through harbrr end-to-end

## Phase 4 — Live smoke (closes the MVP)

- [ ] 5 real trackers, live login/session, gentle rate
- [ ] **Lazy login**: log in only when a search response looks logged-out (Jackett's behavior), then
      retry once — replacing the eager once-per-Engine login established in Phase 2 (which logs in on
      the first search regardless; see `parity/testdata/README.md` "Eager login")
- [ ] **.NET-compatible URL encoder**: replace `url.QueryEscape` in the query/path value encoders so
      `*()'!` match `WebUtility.UrlEncode` (Phase 2 leaves these escaped; see `parity/testdata/README.md`
      "Known divergences")
- [ ] Fetch/auth matrix rows as available: Cloudflare/FlareSolverr (pluggable solver) · 2FA/manual-cookie
- [ ] Docker image + config file

> **MVP = Phases 1–4 (+ Docker/config).** This is the point the central risk is retired. Do not start
> Phase 5+ before the parity gate is green.

## Phase 5 — Operational safety

- [ ] Timeouts, backoff, per-indexer rate limits (anti-blacklist)
- [ ] Health/status; secret redaction audited end-to-end

## Phase 6 — Scale coverage

- [ ] Broaden response-mode and definition coverage; expand selector/date edge-case fixtures
- [ ] **Complete the download resolver**: `.DownloadUri` template namespace, `before.inputs`/
      `before.pathselector`, download-selector template eval, `download.infohash`/`method: post`/
      `headers`, `testlinktorrent` (Phase 2 ships selectors + `before.path`; see `parity/testdata/README.md`)
- [ ] **XML backend edge parity**: CDATA / mixed-namespace / AngleSharp-vs-cascadia edge cases beyond the
      common RSS/Newznab shapes Phase 2 covers
- [ ] Native **Avistaz** family (post-parity; the one family the corpus doesn't cover)

## Phase 7 — Product polish

- [ ] *arr application sync (qui-as-app), Jackett/Prowlarr migration import
- [ ] Native **harbrr → autobrr push** (closes the RSS-polling gap; family-only win)
- [ ] cross-seed search backend; stats; notifications; web UI
- [ ] Postgres behind the existing `dbinterface` (only now)

---

## Standing rules while building (see AGENTS.md)

- Never hand-edit `internal/indexer/definitions/vendor/` — absorb differences in the engine.
- Never log/commit secrets. Always `-race -count=1`. Keep functions small (the linters enforce it).
- One plan item per commit; conventional-commit messages; no AI attribution lines.
