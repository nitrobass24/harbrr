# Phase 9 implementation prompt — Live validation & acceptance (alpha gate)

Paste the block below into an `ultracode` session to run **Phase 9** of harbrr — the
end-of-alpha **live** pass. It is **operator-resourced** and runs through the
build-tagged `internal/smoke` harness (`//go:build smoke`, `SMOKE_*` env creds,
gentle rate, **never CI**). It exercises **every auth/fetch pattern against real
trackers** (Cardigann + the native AvistaZ family) and parity-checks harbrr against a
**live Prowlarr** — the single home for every `[Tracked: Phase 9]` live retest the
offline gates couldn't cover (Phase-5 deferred several auth patterns; Phase-6 the live
half of timeouts/proxy/FlareSolverr; Phase-7 the resolver-needing grabs; Phase-8 the
native AvistaZ family).

It is **not** a code-writing phase. The engine stays **frozen**: a bug it surfaces is
recorded `[Tracked]` against the owning layer with a disposition — fixes are scoped
follow-ups, never ad-hoc edits during validation.

---

## Operator quick-start (automated + repeatable — no tracker-picking, no typing creds)

**You never paste a credential into chat.** `scripts/prowlarr-extract-creds.sh --env`
reads every tracker's creds out of `prowlarr.db` and emits shell-safe `export SMOKE_*`
lines (Cardigann field values map 1:1 to harbrr settings; AvistaZ by Implementation;
the Prowlarr API key auto-extracted). `scripts/phase9-smoke.sh` eval's them into the
harness's env and runs it — secrets never touch chat or disk, and the harness writes
only secret-free evidence.

**One-time setup:** deploy harbrr (`docker-compose.example.yml`), do first-run setup at
`http://<host>:7474/api/docs`, mint a Torznab API key (`POST /api/apikeys`).

**The repeatable run** (idempotent — re-run anytime):

```sh
cp /path/to/config/prowlarr.db /tmp/prowlarr.db   # copy if Prowlarr is running
SMOKE_HARBRR_URL=http://192.168.10.220:7474 \
SMOKE_HARBRR_APIKEY=<minted key> \
SMOKE_PROWLARR_URL=http://192.168.10.220:9696 \
SMOKE_PROWLARR_APIKEY=<Prowlarr → Settings → General → API Key> \
PROWLARR_DB=/tmp/prowlarr.db \
SMOKE_GRAB=1 \
  scripts/phase9-smoke.sh
```

`SMOKE_PROWLARR_APIKEY` is operator-supplied (it lives in Prowlarr's `config.xml`,
not the DB); everything else (tracker creds, FlareSolverr/proxy config) auto-extracts.

It pulls every indexer's creds, adds each to harbrr (encrypted at rest), probes login
(Test action), searches, diffs vs Prowlarr, and writes secret-free evidence to
`internal/smoke/testdata/`. Each tracker is an isolated subtest, so one that fails
(e.g. a def harbrr doesn't vendor, or a stale cred) is reported without aborting the
run.

**Inspect the mapping** (human-readable, no env, for verifying creds parsed correctly):
```sh
scripts/prowlarr-extract-creds.sh /tmp/prowlarr.db
```

FlareSolverr and HTTP/SOCKS proxies are pulled from Prowlarr's `IndexerProxies` table
and applied by **shared tag**: a Cloudflare tracker gets `solver_type=flaresolverr` +
`flaresolverr_url`, a proxied one gets `proxy_type` + `proxy_url` — automatically, no
hand-editing.

**Caveats:** AvistaZ field mapping (username/password/pid by Implementation) is
best-effort — verify with the human-readable mode the first time. A proxy with **no
tags** is treated as applying to all indexers (Prowlarr's convention) — confirm that
matches your setup. SOCKS4 is unsupported (skipped). The harness does not pass a
per-indexer `baseUrl`, so a multi-domain def uses its default link.

The agent's STEP-0 intake below is for the **non-secret** mapping (which resources
exist, which patterns to label) and read-only connectivity checks — not the secret
values, which only ever live in your env via the extractor.

---

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** write code or send any state-mutating live
request until the plan is approved. Two things happen, in order.

### 0a — Prompt me for the live resources + test bed (do this FIRST)

Phase 9 is the live phase. Before planning, prompt me (AskUserQuestion / direct
questions) for, and securely intake, what is available — mapped to the pattern matrix
in the WORK LIST. For **each** pattern, get the tracker **name** and confirm the
choice; then tell me which **credential fields** it needs so I set the matching
`SMOKE_SETTINGS_<SLUG>` env var myself (Operator quick-start) — do NOT ask me to paste
the secret values into chat:

- **user/pass form-login** tracker
- **cookie / 2FA (manual-cookie)** tracker
- **.NET-quirk** tracker (inputs exercising `*()'!` / unicode / `regexp2`)
- **Cloudflare** tracker **+** a reachable **FlareSolverr** URL (e.g. `http://flaresolverr:8191`)
- a tracker reachable via an **HTTP proxy** and one via a **SOCKS5** proxy (+ the proxy URLs)
- an **AvistaZ-family** account (username + password + **PID**) for ≥1 of AvistaZ /
  CinemaZ / PrivateHD / ExoticaZ — *(keep AvistaZ in scope; if unavailable on the day,
  record it `[Tracked: Phase 9]` rather than dropping it)*
- a **resolver-needing** tracker (a `download` block) for the `/dl` grab
- a **broad set** of trackers for the differential at scale
- **Prowlarr** (base URL + API key — the differential oracle) with the same trackers configured
- **qBittorrent** (+ optionally **Sonarr/Radarr**) for the grab half — left **seeding, no hit-and-run**

**Credential-handling rules (NON-NEGOTIABLE — AGENTS.md):**
- Never write a credential / proxy password / FlareSolverr-URL-with-auth / cookie to a
  repo file, the plan, a fixture, a commit, or a log. They live ONLY in env vars + the
  daemon's encrypted store, and in working memory for the run.
- Never echo a credential back in plaintext — refer to them by tracker/field name.
- Enter every credential into the **running daemon via the management API** (`POST
  /api/indexers`) so it lands **AES-256-GCM-encrypted**; the harness does this from
  `SMOKE_SETTINGS_<SLUG>` / `SMOKE_KEY_<SLUG>` (see below).
- Any captured fixture/evidence is **secret-scrubbed before** it is written (the
  harness `validateNoSecrets` refuses to write a record containing a credential token).
- You MAY do **read-only** connectivity checks in plan mode (Prowlarr ping, a
  FlareSolverr `sessions.list`, a proxy reachability probe, an *arr `system/status`).
- To pull existing tracker creds out of Prowlarr (its REST API masks them), use
  `scripts/prowlarr-extract-creds.sh /path/to/prowlarr.db` — it reads the plaintext
  `Indexers.Settings` JSON from the DB. Its output carries secrets: terminal only.

### 0b — Produce ONE complete plan

Cover the whole matrix: which patterns are **resourced** (run live) vs **deferred**
`[Tracked: Phase 9]` (no resource on the day); the differential method + tolerance;
the per-pattern grab; the evidence-capture plan; and the engine-frozen rule. Pressure-
test it with a validator agent, present with `ExitPlanMode`, and wait.

---

## READ FIRST

- `AGENTS.md`; `docs/plan.md` (Phase 9 + the Phase-5 execution-protocol blockquote);
  `docs/phase5-setup.md` (the harness setup + differential pass criteria);
  `docs/architecture.md` (invariant #2 parity, #3 two contracts); `docs/divergences.md`
  (the INDEX of dispositions).
- The `[Tracked: Phase 9]` entries that ARE this phase's checklist — list them:
  ```sh
  grep -rn '\[Tracked: Phase 9' \
    internal/smoke/README.md \
    internal/indexer/registry/testdata/README.md \
    internal/indexer/native/avistaz/testdata/README.md
  ```
- The harness you run: `internal/smoke/smoke_test.go` (build-tagged; the env contract
  is in its doc comment — `SMOKE_TRACKERS` now takes a 4th `pattern` field, and
  per-tracker `SMOKE_SETTINGS_<SLUG>` is a JSON object of any harbrr settings:
  `cookie`/`solver_type`/`flaresolverr_url`/`proxy_type`/`proxy_url`/`username`/
  `password`/`pid`/`apikey`; `SMOKE_GRAB=1` adds the grab-resolve). It already runs a
  **Test action** login probe + the **Prowlarr differential** per tracker.
- `docker-compose.example.yml` to run harbrr next to the *arr stack;
  `scripts/prowlarr-extract-creds.sh` for credential intake.

## CONTEXT (what shipped — Phases 1–8b, all on `main`)

The daemon is complete and offline-proven: the Cardigann engine (parity gate),
operational safety (per-host rate limits, 429/503 backoff, per-indexer proxies,
health/status, the FlareSolverr solver), the full download resolver + the `/dl` grab
proxy, the native AvistaZ family, **and** the Phase-8b JSON management API browsable at
`/api/docs`. Every Phase 9 item is a **live confirmation** of something already
offline-tested — not new behavior.

## HARD RULES (do not work around)

- **Live creds**: per STEP 0 — encrypted store only, redacted everywhere, evidence
  scrubbed before write, never logged/committed/echoed.
- **Gentle rate**: sequential, low concurrency, sane delays, respect each def's rate
  limit. On a rate-limit / anti-bot / ban signal, **back off and report** — never
  hammer or risk an account. The CF solve is heavy (one headless browser/session) —
  reuse the session.
- **Integration gate, never CI**: the harness is build-tagged (`//go:build smoke`),
  env-cred-driven, run manually. It must never run in normal CI or require committed
  secrets. CI stays fully offline.
- **Engine FROZEN**: a bug surfaced here is `[Tracked]` against the owning layer with a
  disposition — do NOT fix it inline. Fixes are separate, scoped, offline-gated PRs.
- **SQLite only; two contracts stay separate; no AI attribution.** Carry every prior
  phase's hard rules forward.

## WORK LIST — the matrix (each row: resourced→run live, or `[Tracked: Phase 9]`)

1. **Every auth/fetch pattern live**, each against an operator-supplied tracker, via
   the harness (Test-action login probe + search + Prowlarr differential):
   - user/pass **form login** (confirm logged-out → relogin, i.e. lazy login works live)
   - **cookie / 2FA** via `solver_type=manual_cookie` + the encrypted `cookie`
   - **.NET-quirk** — a query exercising `*()'!` / unicode / `regexp2` constructs
   - **Cloudflare via FlareSolverr** — `solver_type=flaresolverr` clears a real CF
     tracker end to end
   - **per-indexer proxy** — `proxy_type=http` and `proxy_type=socks5` each route a real search
2. **Broad live Prowlarr differential** — many trackers (not just the Phase-5 five),
   **Cardigann + AvistaZ**: same query → Prowlarr feed vs harbrr feed → diff within the
   harness tolerance, confirming request/response + category parity at scale.
3. **AvistaZ family live** — search + grab + the Prowlarr differential for ≥1 site;
   **confirm the real `created_at_iso` format** (the parser tries four layouts) and
   **verify download-URL path-key redaction** holds. (`native/avistaz/testdata/README.md`.)
4. **Grab end-to-end per pattern** — search → resolved `.torrent` → **seeding in
   qBittorrent (left seeding, no hit-and-run)**, for ≥1 tracker per auth pattern,
   **including a resolver-needing tracker via the Phase-7 `/dl` path**. (`SMOKE_GRAB=1`
   resolves to the `.torrent`; the qBit push + seed is the manual no-H&R step.)
5. **Acceptance** — every pattern green, or its gap recorded `[Tracked]` with a
   disposition. This is the live half of "match Jackett/Prowlarr on real trackers"; the
   offline parity gate (Phase 2) already proves it deterministically.

## ORACLE / EVIDENCE

- **Prowlarr differential** (the result-set gate) + the **Test action** (the live login
  confirmation per credentialed pattern) + the **grab** (resolve to `.torrent`, manual
  qBit seed). Tolerances are the Phase-5 ones (`docs/phase5-setup.md`): live data is
  non-deterministic and harbrr category-filters, so it's bounded, not byte-exact.
- Evidence is captured **secret-free** per tracker under `internal/smoke/testdata/`
  (gitignored) and summarized in `internal/smoke/README.md` (counts, pass/fail,
  pattern, testOK, grab result, the differential note) — never creds, never raw feeds.

## SUCCESS CRITERIA — assert as a gate

- Each auth/fetch pattern is **live-confirmed** (Test passes + the differential passes)
  or recorded `[Tracked: Phase 9]` with the reason it couldn't run.
- The **broad differential** passes at scale (Cardigann + AvistaZ) within tolerance.
- A **grab seeds in qBittorrent** (no H&R) for ≥1 tracker per pattern, including a
  resolver-needing one via `/dl`; no passkey/Bearer/cookie ever appears in the feed,
  the served link, a log, an error, or an evidence file.
- AvistaZ: the real `created_at_iso` format is confirmed (or the divergence `[Tracked]`)
  and the download-URL path-key redaction holds.
- **No credential** is logged, committed, or echoed; the live harness needs **no
  committed secrets** and never ran in CI.

## AFTER ALL ITEMS

- f) **Record outcomes.** For each `[Tracked: Phase 9]` entry in the per-layer READMEs,
  flip it to `[Resolved: Phase 9]` (live-confirmed) or keep `[Tracked]` with an explicit
  disposition (resource unavailable / a surfaced bug). Update `docs/divergences.md` only
  by pointing at the layer README (it is an INDEX). Promote the now-live-proven items in
  `docs/highlights.md` from `[partial]`/`[planned]` to `[shipped]` with the evidence
  citation. Tick the Phase 9 boxes in `docs/plan.md` that are green.
- g) **No PR of engine changes.** If a bug was found, open a *separate* scoped,
  offline-gated fix PR — never bundled with the validation run.
- h) **FINAL REPORT**: per-pattern live result (Test + differential + grab) or its
  `[Tracked]` disposition; the AvistaZ findings; explicit confirmation no credential was
  logged/committed; the acceptance verdict; and which `[Tracked: Phase 9]` entries are
  now `[Resolved]`.
