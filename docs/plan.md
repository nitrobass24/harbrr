# seekbrr build plan

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
- [ ] Wire `cobra`/`viper` entrypoint and a typed config struct (no `map[string]any`)

## Phase 1 — Engine proof (offline) — *retires the existential risk*

Build the pipeline stage by stage, each table-driven-tested with its own fixtures. Keep stages
decoupled.

- [ ] **loader** — parse + schema-validate a definition into a typed model; precedence dropin > vendor
- [ ] **mapper** — capabilities document + category mapping (Newznab category system)
- [ ] **template** — Go `text/template` with .NET-equivalent truthiness (empty-vs-missing)
- [ ] **filter** — the bounded filter registry; start with the 6 dominant ops (`re_replace`, `replace`,
      `append`, `dateparse`, `regexp`, `querystring`), then the tail
- [ ] **selector** — HTML (`cascadia`/`goquery`) + JSON selection; start the standing selector fixture
      suite (vs Jackett semantics)
- [ ] **dateparse** — .NET format strings → Go layout; cover timezones, relative dates, localized names
- [ ] **regexadapter** — RE2 default; route to `regexp2` on opt-in / non-Latin `language:` / RE2
      compile-failure / .NET-only constructs; run both engines on shared fixtures
- [ ] **login/session executor** — `form`/`post`/`get`/`cookie`, CSRF, cookie jar, re-login;
      manual-cookie fallback. Test offline against saved login sequences
- [ ] **normalizer** — produce normalized release objects (canonical, deterministic JSON)
- [ ] Engine assembles the stages end-to-end on a saved response

## Phase 2 — Offline parity — *the gate*

- [ ] Port Jackett's GPL-2.0 Cardigann engine tests (`CardigannIndexerHtmlTests`/`JsonTests`)
- [ ] Build the differential harness (run Jackett + seekbrr on the same saved bytes; capture goldens)
- [ ] Wire `internal/indexer/cardigann/parity` to the real engine (replace the stub `Process`)
- [ ] Pass the **compatibility matrix** offline rows (each archetype has a fixture):
  - [ ] HTML / form login
  - [ ] HTML / cookie login
  - [ ] JSON-API
  - [ ] XML / Newznab
  - [ ] non-Latin-script (regexp2 path)
  - [ ] freeleech (download/uploadvolumefactor)
  - [ ] multi-category
  - [ ] date-heavy (multiple .NET formats + relative)
  - [ ] magnet-only (magnet/infohash synthesis)
  - [ ] download-link pre-request
- [ ] **Success criteria met:** 100% defs load w/o panic · zero silent schema failures (triaged to a
      visible skip-list) · ported Jackett tests pass · matches Jackett on ≥25 saved fixtures · secrets
      redacted in logs · broken indexers degrade cleanly

## Phase 3 — Minimal Torznab output

- [ ] `internal/torznab`: capabilities document + `t=caps|search|tvsearch|movie|music|book`
- [ ] **caps/category correctness is a gate** (Sonarr/Radarr failures usually trace here)
- [ ] Sonarr/Radarr can search a handful of real trackers through seekbrr end-to-end

## Phase 4 — Live smoke (closes the MVP)

- [ ] 5 real trackers, live login/session, gentle rate
- [ ] Fetch/auth matrix rows as available: Cloudflare/FlareSolverr (pluggable solver) · 2FA/manual-cookie
- [ ] Docker image + config file

> **MVP = Phases 1–4 (+ Docker/config).** This is the point the central risk is retired. Do not start
> Phase 5+ before the parity gate is green.

## Phase 5 — Operational safety

- [ ] Timeouts, backoff, per-indexer rate limits (anti-blacklist)
- [ ] Health/status; secret redaction audited end-to-end

## Phase 6 — Scale coverage

- [ ] Broaden response-mode and definition coverage; expand selector/date edge-case fixtures
- [ ] Native **Avistaz** family (post-parity; the one family the corpus doesn't cover)

## Phase 7 — Product polish

- [ ] *arr application sync (qui-as-app), Jackett/Prowlarr migration import
- [ ] Native **seekbrr → autobrr push** (closes the RSS-polling gap; family-only win)
- [ ] cross-seed search backend; stats; notifications; web UI
- [ ] Postgres behind the existing `dbinterface` (only now)

---

## Standing rules while building (see AGENTS.md)

- Never hand-edit `internal/indexer/definitions/vendor/` — absorb differences in the engine.
- Never log/commit secrets. Always `-race -count=1`. Keep functions small (the linters enforce it).
- One plan item per commit; conventional-commit messages; no AI attribution lines.
