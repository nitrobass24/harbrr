# Phase 8 implementation prompt — Native Avistaz family

Paste the block below into an `ultracode` session to implement **Phase 8 (Native Avistaz family)** of
harbrr — the **first and only native indexer driver** in the project, the deliberate exception to
harbrr's "embed Jackett defs, absorb differences in the engine" model. The AvistaZ network (AvistaZ /
CinemaZ / PrivateHD / ExoticaZ) has **0 Cardigann defs** because its **login→Bearer `api/v1/jackett`
auth exceeds the declarative format**, so it must be written as Go code. It is **post-Cardigann-parity,
scheduled (not demand-gated)** — these trackers are widely used, and autobrr's family only reaches them
via RSS today; harbrr adds the **search** path.

It is **one PR** off fresh `main`: **`phase8/native-avistaz`**. A native driver is a small,
self-contained subsystem (one shared base + four site shells + fixtures), so it does not need the
two-PR split Phase 7 used. If the offline fixtures unexpectedly balloon past the CodeRabbit **150-file
cap**, split the fixtures into a second PR and state the merge order.

It stays **offline-gated**: the gate is a stub auth/API server + canned JSON fixtures whose goldens are
derived from **Prowlarr's (and Jackett's) documented parse contract**, never captured from a live
AvistaZ. The live Prowlarr differential + a real grab are the **Phase 9** gate.

---

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, edit files, or send any live
request until the plan is approved.

### 0a — Prompt me for the (optional) live resource (do this FIRST)

Phase 8 is **offline-gated**: every box closes on committed deterministic fixtures regardless of any
live resource. The live confirmation — a real search + grab against a real AvistaZ-network account,
plus the Prowlarr differential — is the **Phase 9** gate, not this phase. So ask me, but default to
deferring:

- **AvistaZ-family credentials** (username + password + PID for one of the four sites), entered into the
  running daemon's encrypted store via the API — never chat/repo — for an *optional* end-to-end live
  confirmation. If I can't supply them on the day, record `[Tracked: Phase 9 — live validation]`; do
  **not** fake it, capture a real API response into the repo, or manufacture an empty commit.
- The **Phase-5 test bed** (Prowlarr as the differential oracle, qBittorrent for the grab) only if we do
  the optional live confirmation.

### 0b — Produce ONE complete plan, re-verifying BOTH sets of seams

The plan must pressure-test the DECIDED architecture below against (1) the **current harbrr seams** (the
`torznab.Indexer` interface, the registry build path, the normalizer release shape, the paced client,
the `/dl` proxy — these moved in Phase 7; trust the named symbol and re-locate it) and (2) the **current
Prowlarr Avistaz source** (re-pull it — the recon below is pinned to Prowlarr `d6e8466` / Jackett
`69edf7b`; if much time has passed, re-read the real files, do not trust this summary blindly). Present
with `ExitPlanMode` and wait for approval.

---

## READ FIRST

- `AGENTS.md` (prime directive + non-negotiables), `docs/plan.md` (Phase 8 box), `docs/ideas.md` §6
  (native indexers — a post-parity milestone) + §11 (autobrr family integration), `docs/architecture.md`
  (read before touching the registry/provider seam), `docs/divergences.md` (an **INDEX**, not a
  free-text append target).
- **harbrr integration seams (reuse, do NOT re-invent — Phase 7 left these in place):**
  - `internal/web/torznab/provider.go` — the `Indexer` interface the driver must satisfy:
    `Info() IndexerInfo`, `Capabilities() *mapper.Capabilities`, `Search(ctx, search.Query)
    ([]*normalizer.Release, error)`, `NeedsResolver() bool`, `Grab(ctx, link) (*search.GrabResult,
    error)`. The native driver implements this directly (no Cardigann engine behind it).
  - `internal/indexer/registry/registry.go` (`build`/`resolve`) + `internal/indexer/registry/adapter.go`
    — where a slug resolves to a `torznab.Indexer` today (a Cardigann engine via `indexerAdapter`).
    Phase 8 adds a branch: an AvistaZ-family instance builds the native driver instead. Health recording
    (`recordHealth`) and the paced client must apply to it too.
  - `internal/indexer/registry/pacedclient.go` — the per-host paced HTTP client (rate limits + 429/503
    backoff). Avistaz's **6s request delay** is configured here, not hand-rolled.
  - `internal/indexer/cardigann/normalizer` `Release` — the canonical, deterministic release struct the
    serializer consumes; the driver produces these (Title/Link/Size/Categories/Seeders/Leechers/Peers/
    Grabs/Files/PublishDate/DownloadVolumeFactor/UploadVolumeFactor/MinimumRatio/MinimumSeedTime/
    IMDBID/TMDBID/TVDBID/InfoHash…). Do NOT invent a parallel release type.
  - `internal/indexer/cardigann/mapper` — `Capabilities` + the Newznab category system the caps document
    advertises; reuse it to build each site's caps.
  - `internal/secrets` (`Keyring.Encrypt/Decrypt`) — the encrypted credential store; username/password/
    PID live here.
  - `internal/http` (`RedactURL`/`RedactError`, the `Doer` seam) — the redaction chokepoints. The Bearer
    token, PID, password, and any key embedded in a download URL are secrets.
  - `internal/web/torznab` `/dl` proxy + `search.GrabResult` — the grab-time proxy Phase 7 built. The
    AvistaZ download requires a Bearer header *arr cannot send, so the driver sets `NeedsResolver()=true`
    and its `Grab` fetches the download URL with the Bearer header; the feed routes through `/dl` exactly
    as a resolver-needing Cardigann tracker does. This is reuse, not new infrastructure.
  - `internal/indexer/native/doc.go` — the package home (a stub today). The driver lives under
    `internal/indexer/native/avistaz/`.
- **The Avistaz contract (recon — re-pull to confirm; Prowlarr `d6e8466`, Jackett `69edf7b`):**
  Prowlarr `src/NzbDrone.Core/Indexers/Definitions/Avistaz/` — `AvistazBase.cs` (auth + lifecycle),
  `AvistazApi.cs` (JSON DTOs), `AvistazParserBase.cs` (parse), `AvistazRequestGenerator.cs` (search),
  `AvistazSettings.cs` (creds), and the per-site subclasses `Definitions/{AvistaZ,CinemaZ,PrivateHD,
  ExoticaZ}.cs`. Jackett's `Indexers/Definitions/Abstract/AvistazTracker.cs` is the same shape (cross-
  check). The contract summary is in **CONTEXT** below — but the SOURCE is the oracle; read it.

---

## CONTEXT (the Avistaz technical contract)

Phases 1–7 proved + completed the Cardigann engine (parity, operational safety, the download resolver +
`/dl` proxy). Phase 8 is the first **native** driver. UNIT3D (~80 defs) and Gazelle (~6 defs) are
covered by the vendored corpus and are NOT built natively; **only AvistaZ (0 defs) is**. Release-name
parsing is **not** ported (the family's `rls` does it). The driver reuses every harbrr seam above; it is
thin.

**Auth.** `POST {BaseUrl}api/v1/jackett/auth`, **form-encoded** (`application/x-www-form-urlencoded`)
fields `username`, `password`, `pid` (Prowlarr `.Trim()`s `pid`; Jackett trims all three — trim all
three). Response JSON `{ "token": "…" }`. Attach `Authorization: Bearer {token}` to every search +
download request. **No TTL is tracked; refresh is reactive** — on a `401 Unauthorized` or `412
Precondition Failed` response, re-auth once and retry. `429` = rate-limit error; an auth error body
deserializes `{ "message": … }`. Cache the token on the driver instance (persisting across restarts is
optional polish).

**Settings.** `username`, `password`, `pid` (all required, all secrets), plus a `freeleech_only`
checkbox. Base URL is per-site (below).

**Search.** `GET {BaseUrl}api/v1/jackett/torrents` with: `in=1` (constant), `type` (single tracker
category id — **1=Movies, 2=TV** — or `0`), `limit` (≤ `PageSize`=50), `page` (1-based, only when
offset+limit set), and the query, mapped **per search type, ID-preferred and mutually exclusive**:
- **movie**: `imdb={ttNNNNNNN}` else `tmdb=` else `search={term}` (an ID query sends **no** `search`).
- **tv**: `imdb={ttNNNNNNN}` **+** `search={episodeTerm}`; else `tvdb=` **+** `search={episodeTerm}`;
  else `search="{term} {episodeTerm}"`. Season/episode is folded into `search`, never separate params.
- **music / basic**: `search={term}`. **book**: unsupported (empty).
- `freeleech_only` → `discount[]=1`. A resolution-specific category → `video_quality[]` (`1`=SD, `2`=720p,
  `3`=1080p, `6`=2160p, `7`=1080i). Optional `tags={genre}`.

**Parse.** Response `{ "data": [ … ] }`; per release: **Title = `file_name`** (NOT `release_title`),
`DownloadUrl = download`, `Guid/InfoUrl = url`, `Size = file_size`, `Files = file_count`, `Grabs =
completed`, `Seeders = seed`, **`Peers = leech + seed`**, `PublishDate = parse(created_at_iso,
invariant, UTC)`, `InfoHash = info_hash`, **`DownloadVolumeFactor = download_multiply` /
`UploadVolumeFactor = upload_multiply`** (freeleech IS the multipliers — no separate flag), `MinimumRatio
= 1`, and a **size-based `MinimumSeedTime`** (`>50 GiB`: `(100*ln(GiB) - 219.2023)*3600`; else `259200 +
GiB*7200`). IDs from nested `movie_tv { imdb, tmdb, tvdb }`. **Categories computed from `type` +
`video_quality`**: `type` strings are `MOVIE` / `TV-SHOW` (hyphen) / `MUSIC`; HD = {1080p,1080i,720p} →
MoviesHD/TVHD, `2160p` → UHD, else SD; MUSIC → Audio. Results **sorted by PublishDate descending**.
`404` = empty results (not an error), `429` = rate-limit error, non-JSON / non-200 = error.

**Download.** Direct `download` URL, **fetched with the Bearer header** (the framework attaches it). The
URL may embed a per-user key. So the driver's `Grab` fetches it with the Bearer + redaction;
`NeedsResolver()=true` routes the feed through `/dl` so neither the Bearer nor any embedded key reaches
the feed.

**The four sites (one base + four shells):**
- **AvistaZ** — `https://avistaz.to/`; Movies(1)/TV(2) caps incl. tvdb/tmdb; **only quirk:** seasonless
  episode override — `Season is null/0 && Episode set → "E{episode}"`, else the standard `SxxEyy`.
- **CinemaZ** — `https://cinemaz.to/`; same Movies/TV map; caps **omit tvdb & tmdb**; no other override.
- **PrivateHD** — `https://privatehd.to/`; identical to AvistaZ **minus** the episode override.
- **ExoticaZ** — `https://exoticaz.to/`; **adult (XXX)**; **no TV/Movie search params** (basic/search
  only); its own **8-entry XXX category map** read from the response `category` dict via a **parser
  variant** (categories come from the `category` field, not `type`+`video_quality`).
- `AnimeZ` is NOT part of this family (it is Cardigann-based) — do not pull it in.

**Rate limit / quirks:** 6s between requests (both Prowlarr and Jackett), `PageSize=50`, pagination
effectively single-page, `429`→rate-limit error, `404`→empty. **Prowlarr logs creds + token; harbrr must
NOT** — redact all four (username/password/pid/token) and any download-URL key everywhere.

---

## HARD RULES (do not work around)

- **Redaction is absolute.** The username, password, PID, Bearer token, and any key in a download URL are
  secrets. Never log/print/commit them; route every error/log through `internal/http` redaction. Prowlarr
  logs them (`LogResponseContent = true`) — **do not copy that**; assert redaction with a test.
- **The native driver satisfies the EXISTING `torznab.Indexer` interface** and plugs into the existing
  registry/serializer/`/dl` path. Do NOT special-case the Torznab handler for AvistaZ, fork the
  serializer, or invent a parallel release type — produce `normalizer.Release`. Keep the interface ≤5
  methods.
- **The oracle is Prowlarr/Jackett's documented behavior, offline.** Goldens are **derived** from the
  parse contract (request params, field mapping, category derivation, MinimumSeedTime) — never captured
  from a live AvistaZ (no creds in CI, no real API JSON in the repo). Synthetic fixtures only.
- **Honor the 6s rate limit via the paced client** — never hammer the API; the auth + search + grab all
  go through the paced doer.
- **SQLite only**; pure-Go; the two HTTP contracts stay separate (the driver is a serving-tree data
  source; creds/CRUD are the management API).
- **NO AI attribution/co-author/"Generated with" lines.** Conventional commits; gofumpt-clean; no
  `map[string]any` for the API DTOs (typed structs); split god-functions (funlen/gocyclo).
- **Branch & box.** One PR off `main` on `phase8/native-avistaz`; NEVER touch `main`. Tick the Phase 8
  `docs/plan.md` box in the **same commit** that lands the working four-site driver + its offline gate,
  only when green. Enabling-only commits (the package skeleton, settings) tick NO box — say so.

---

## ORACLE / FIXTURES (decided): OFFLINE + deterministic; live → Phase 9

**Offline deterministic (committed; runs in CI — the gate):**

- **Auth** — a **stub HTTP server / replay Doer**: assert the driver POSTs **form-encoded**
  `username/password/pid` to `…/api/v1/jackett/auth`, reads `{token}`, attaches `Authorization: Bearer …`
  to the next request, and on a `401`/`412` re-auths once and retries. A **redaction test** asserts none
  of the four secrets appears in a log/error.
- **Search request construction** — a recording Doer captures the exact query string the driver builds;
  assert it matches the Prowlarr contract for each search type (movie imdb/tmdb/search; tv
  imdb/tvdb+episode; music; freeleech `discount[]`; `video_quality[]`; AvistaZ's `E{episode}` override;
  ExoticaZ basic-only). This is the strongest parity signal — pin it.
- **Response parse** — table-driven over **synthetic** `{data:[…]}` JSON → assert the golden
  `[]normalizer.Release` (title=file_name, peers=leech+seed, the multipliers, ids from `movie_tv`,
  MinimumSeedTime formula, PublishDate UTC, category derivation per type+video_quality, sort-desc).
  Include the `404`→empty and `429`→error cases.
- **Per-site** — each of the four sites: caps document advertises the right categories/modes (CinemaZ
  omits tvdb/tmdb; ExoticaZ advertises XXX + no TV/movie params); ExoticaZ parses categories from the
  `category` dict.
- **Grab** — the driver's `Grab` fetches the `download` URL with the Bearer header and serves the
  `.torrent` through `/dl`; assert the Bearer + any URL key are redacted and absent from the served feed.

**Operator-resourced LIVE (manual / build-tagged; never in CI → Phase 9):** a real search + grab against
one AvistaZ-network account, plus the Prowlarr differential (same query → Prowlarr feed vs harbrr feed →
diff). Captured secret-free in the smoke README **or** recorded `[Tracked: Phase 9 — live validation]`.

CI stays fully **offline and deterministic** — no live tracker, no network, no real creds.

---

## WORK LIST — items in dependency order (one PR)

1. **Driver skeleton + settings + registry branch.** `internal/indexer/native/avistaz/`: the
   `AvistazBase` driver type implementing `torznab.Indexer` (Info/Capabilities/Search/NeedsResolver=true/
   Grab), the typed settings (username/password/pid/freeleech_only), and the registry `build` branch that
   constructs the native driver for an AvistaZ-family instance (paced client + keyring + health wired in
   like the Cardigann adapter). *(enabling — ticks NO box.)*
2. **Auth.** Form-POST → token; Bearer header; reactive refresh on 401/412; token cached; **redaction**.
   Stub-server + redaction tests. *(enabling — ticks NO box.)*
3. **Search request builder.** The param construction + per-search-type ID mapping; assert via a recording
   Doer against the Prowlarr contract.
4. **Response parser → `normalizer.Release`.** The full field mapping incl. category derivation,
   MinimumSeedTime, sort; 404/429 handling. Golden table tests.
5. **Grab / download.** `Grab` fetches the `download` URL with the Bearer header → `.torrent`;
   `NeedsResolver()=true`; route through `/dl`; redact. Handler/redaction test.
6. **The four sites + caps.** `AvistaZ` (episode override), `CinemaZ` (no tvdb/tmdb caps), `PrivateHD`
   (no override), `ExoticaZ` (XXX map + response-`category`-dict parser variant + no TV/movie params).
   Per-site caps + parse fixtures. **This commit closes the Phase 8 box** (the four-site driver is
   complete and green) — say so.

---

## RISKS (carry into the plan with concrete tests)

- **Auth request byte-mismatch** vs Prowlarr (form vs JSON encoding; field names; pid/all-three trim) —
  pin the exact request with a recording Doer.
- **Token refresh** — a 401/412 must trigger exactly one re-auth + retry, not a loop; test the path.
- **Search param divergence** — the ID-preferred/mutually-exclusive mapping, `video_quality[]`,
  `discount[]`, AvistaZ's `E{episode}`, ExoticaZ basic-only — golden each.
- **Category derivation** — `type`+`video_quality` → HD/UHD/SD vs ExoticaZ's `category` dict; the
  `TV-SHOW` hyphen; unknown type → error. Golden per case.
- **MinimumSeedTime / PublishDate** — port the size formula + invariant-UTC date exactly.
- **Redaction leak** — username/password/pid/token/download-key in a log, error, or the served feed.
  Assert absence (Prowlarr's logging is the anti-pattern).
- **Rate limit** — 6s via the paced client; prove the driver does not bypass it.
- **`/dl` + Bearer download** — the grab must attach the Bearer and never expose it (or a URL key) in the
  feed/redirect; reuse Phase 7's `/dl`, don't re-implement.
- **No Cardigann oracle** — goldens are Prowlarr-derived, NOT live captures; never grade against a live
  AvistaZ, never commit a real API response.

## SUCCESS CRITERIA — assert as a gate

- All four sites register, build, and advertise correct caps (CinemaZ omits tvdb/tmdb; ExoticaZ XXX +
  no TV/movie params), each satisfying `torznab.Indexer`.
- Auth: form-POST → token → Bearer, reactive refresh on 401/412, **all four secrets redacted** (proven).
- Search request params match the Prowlarr contract per search type; parsed releases match goldens
  (title=file_name, peers=leech+seed, multipliers, ids, MinimumSeedTime, sort-desc); 404→empty,
  429→error.
- Grab fetches with the Bearer via `/dl`; no secret in the served feed.
- 6s rate limit honored through the paced client.
- `make precommit` + `make build` green (`-race`); all cross-builds green; contracts separate; SQLite-only;
  PR ≤150 files.
- The live search/grab + Prowlarr differential is captured secret-free **or** recorded `[Tracked: Phase 9
  — live validation]`.

## PER-ITEM LOOP (after plan approval; one commit per item)

For each WORK LIST item: **(a)** brief per-item plan · **(b)** implement + table-driven offline tests
(stub/recording Doer for auth+search+grab; golden DTO→Release fixtures; synthetic JSON) · **(c)** verify
`make precommit` + `make build` (`-race`) · **(d) ≥3 adversarial skeptics** target: auth-request
byte-exactness vs Prowlarr; token-refresh loop/correctness; search-param divergence; category-derivation
parity; a redacted secret leaking; ExoticaZ parser divergence; the 6s limit being bypassed; the `/dl`
Bearer download exposing a secret. Fix every confirmed issue; re-verify. (Fall back to rigorous inline
self-review if skeptic agents die on spend, and say so.) · **(e)** one focused conventional commit; tick
the box only on the box-bearing commit.

## AFTER ALL ITEMS

**(f)** End-to-end review + completeness critic. Record any Avistaz-vs-Prowlarr divergence with a
disposition in a new `internal/indexer/native/avistaz/testdata/README.md` (or the native layer's README)
**and** add ONE row to `docs/divergences.md`'s layer table pointing at it (it's an INDEX — do not
free-text into it). Add the native AvistaZ family to `docs/highlights.md` (`[shipped]`). **(g)** keep the
PR ≤150 files. **(h)** open the PR → `main`; summary + testing checklist + the four-site coverage table;
**no creds/PID/tokens/tracker URLs in the body**; no AI attribution. **(i)** push, CI green. **(j)**
address every CodeRabbit finding (validate → fix → revalidate; mind the ~1h rate limit). **(k) PAUSE** —
once CI + review are green, STOP; do NOT merge; wait for approval.

## FINAL REPORT

State the items shipped (commit ids); the four sites + their caps as built; the offline coverage by area
(auth stub + redaction, search-param construction, DTO→Release goldens, per-site caps, ExoticaZ parser,
`/dl` Bearer grab); the live search/grab + Prowlarr-differential result or its `[Tracked: Phase 9]`
disposition; explicit confirmation that no username/password/PID/token/download-key appears in a log,
error, the served feed, or a commit (redaction holds end-to-end); known divergences + dispositions; and
any open questions. State which required checks ran, which were skipped/deferred and why.
