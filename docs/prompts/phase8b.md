# Phase 8b implementation prompt — Complete the management API

Paste the block below into an `ultracode` session to implement **Phase 8b** of harbrr — closing the
control-plane/data-plane gaps in the JSON **management API** so an operator (and a future web UI, which
is by design an API client — harbrr is headless + API-first) can **search, read capabilities, discover a
definition's settings schema, and change the admin password** through the documented API at `/api/docs`.

The full gap analysis, the in-scope list, and the explicit exclusions are in **`docs/issues/phase8b.md`
— read it first.** This is **product surface, post-engine-parity**: it adds JSON endpoints that **reuse**
the proven engine/registry and the existing Torznab param-mapping + `/dl` tokenizer. It does **not** touch
the Cardigann engine, the native AvistaZ drivers, or the Torznab XML feed's behavior.

It is **one PR** off `main`: **`phase8b/management-api`**. If it unexpectedly exceeds the CodeRabbit
**150-file cap**, split the spec/tests into a second PR and state the merge order.

---

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, or edit files until the plan is
approved.

- **Re-verify the seams** below against the CURRENT code — line numbers drift; trust the named symbol and
  re-locate it. The recon in this prompt came from a verification pass; confirm each gap is still real and
  each reuse still exists before planning.
- **Re-confirm the redaction path** end to end: how the Torznab feed keeps a passkey-bearing download link
  out of the served XML (`NeedsResolver()`, `dlRewriter`, `encodeDLToken`) — the JSON search must do the
  same. This is the highest risk in the phase.
- **Produce ONE complete plan**: the work items, each endpoint's exact contract (params, JSON response
  schema, auth, errors), the redaction + drift-test strategy, and the test plan (parity + redaction +
  per-endpoint handler tests). Present with `ExitPlanMode` and wait for approval.

---

## READ FIRST

- `AGENTS.md` (prime directive + non-negotiables), `docs/issues/phase8b.md` (the gap analysis + scope +
  exclusions), `docs/architecture.md` (**invariant #3** — the JSON management API and the Torznab XML feed
  are separate route trees; do NOT merge them), `docs/divergences.md` (an **INDEX**, not an append target).
- **Seams to reuse (do NOT re-invent):**
  - `internal/web/api/router.go` (`routes()`) — register the new `GET` routes under the **authenticated**
    group (alongside `/api/indexers/{slug}/test|status`); `internal/web/api/middleware.go`
    (`resolveAuth`/`requireAuth`) gives them the X-API-Key/session auth for free.
  - `internal/web/api/indexer_handlers.go` + `auth_handlers.go` — where the handlers live; add the new
    methods here and reuse the existing response shapers (`toInstanceResponse`, `settingResponse`,
    `internal/web/api/encode.go` `writeJSON`/`writeServiceError`).
  - `internal/indexer/registry` — `Registry.Indexer(ctx, slug)` resolves a slug → `torznab.Indexer`;
    `adapter.go` `Search`/`Capabilities` already wrap the engine **and record health events**. The JSON
    handlers call these directly (so health classification + the paced client apply unchanged).
  - `internal/web/torznab/query.go` `buildQuery(url.Values, caps)` — already maps **every** Torznab query
    param (`q`, `imdbid`/`tmdbid`/`tvdbid`/`tvmazeid`/`traktid`/`doubanid`/`rid`, `season`/`ep`, `year`,
    `artist`/`album`/`label`/`track`/`author`/`title`, `cat`→tracker cats, `limit`/`offset`) to
    `search.Query`. **Extract it to a shared package (or reuse it directly) so the JSON search and the
    Torznab handler map params identically** — this is what guarantees parity. `parsePaging` clamps
    `limit` to `[1,100]`.
  - `internal/indexer/cardigann/normalizer` `Release` — has full JSON tags; **marshal it directly, do not
    invent a parallel DTO**. Note: no `omitempty` on `size`/`seeders`/`leechers`/`peers`/volume factors —
    `0` is meaningful (freeleech, zero-seed).
  - `internal/web/torznab/dltoken.go` (`encodeDLToken`) + `handler.go` `dlRewriter`/`dlBaseURL` — the
    passkey-sealing `/dl` tokenizer. **The JSON search MUST run a resolver-needing indexer's `link`
    through this** so the response carries the opaque `/dl` URL, never the raw passkey link — same as the
    feed. This needs the keyring/dlToken + base path wired into the management router/handler.
  - `internal/indexer/cardigann/mapper` `Capabilities` (`Modes`, `AllowRawSearch`, `AllowTVSearchIMDB`,
    `Categories`, `DefaultCategories`) + `Category` (`ID`, `Name`, `IsCustom`, `IsParent`, `Parent`) — for
    the capabilities endpoint. `internal/torznab/searchmodes.go` is the canonical mode/param token source;
    `internal/torznab/caps.go` `LimitsDefault/Max=100`.
  - `internal/indexer/cardigann/loader` `Loader.Load(id)` (validates the id — reuse its traversal guard) +
    `Definition.Settings` (`[]SettingsField` — `Name`/`Label`/`Type`/`Default`/`Options`) +
    `SettingsField.IsSecret()` + `mapper.Build(def)` — for the definition-detail endpoint. The native
    catalog is `registry.NativeDefinitions()`; compose both sources.
  - `internal/web/swagger/openapi.yaml` — the spec. **Every new route is documented here in the same
    commit.** The drift test `internal/web/api/router_test.go` `TestOpenAPIDriftRoutesMatchSpec` asserts
    `mounted routes == documented operations` (METHOD+path) — it fails if a route is undocumented or a
    documented op is unmounted. (The `/api/auth/oidc/*` routes are mounted-but-`501` and documented, so
    they already pass the drift test — leave them alone; OIDC is deferred to Phase 10.)
  - `internal/http` `RedactURL`/`RedactError` — the redaction chokepoints. `internal/database/users.go`
    `UpdatePassword` + `internal/auth` (password hashing/verification) — for change-password.

---

## CONTEXT (what's missing + the verified contracts)

harbrr is **API-first / headless**; the management API + CLI are the whole interface today, and the
eventual web UI will be an API client. But the documented API is a control plane: search/caps/grab are
Torznab-XML-only, discovery is incomplete, and some config is undocumented. The four new endpoints +
spec hardening make the API complete enough that "everything the UI/operator needs is in the documented
API" becomes true for the control + data plane. See `docs/issues/phase8b.md` for the full inventory.

**`GET /api/indexers/{slug}/search`** — auth (session or `X-API-Key`). Query params = the Torznab set
(via `buildQuery`): `q`, the external ids, `season`/`ep`, `year`, the music/book fields, `cat`
(comma-separated newznab ids), `limit` (≤100), `offset`. Resolve slug → indexer, `buildQuery` →
`search.Query`, `idx.Search(ctx, query)` → `[]*normalizer.Release`; **for a resolver-needing indexer,
rewrite each release's `link` to the `/dl` proxy URL (token) before marshaling**; return the releases as
JSON (the `Release` struct, marshaled directly). `404` unknown/disabled slug; `500` redacted on a search
failure (auth/rate/parse). The result MUST equal the Torznab `t=search` result for the same query.

**`GET /api/indexers/{slug}/capabilities`** — `idx.Capabilities()` → JSON: `modes` (map of mode→param
names), `allowRawSearch`, `allowTVSearchIMDB`, `categories` (flat list: `{id, name, isCustom, isParent,
parent?}`), `defaultCategories`, `limits` (`{default, max}`). The internal `CategoryMap` lookup is not
serialized.

**`GET /api/definitions/{id}`** — `loader.Load(id)` (or the native catalog) → JSON: the summary fields +
`settings` (ordered `[]{name, label, type, default, options, secret}`, `secret` = `IsSecret()`) +
`caps` via `mapper.Build` (modes + categories). Expose the `secret` **flag** and the cleartext default
label (e.g. "Your API Key"); this is the DEFINITION schema, **not** a configured instance's settings, so
there is no instance secret to leak — but never echo a configured instance's stored secret here.

**`POST /api/auth/change-password`** — auth required; body `{currentPassword, newPassword}`; **verify the
current password** (reuse the login verifier), then `UpdatePassword` with the new hash; never log either
value; rotate/renew the session as the login path does. `400` on a bad/short new password; `401` on a
wrong current password (matches the login path's 401 — harbrr's error envelope uses 400/401/404/409/500,
there is no 403).

**Spec hardening** (no new routes): document the reserved/config settings (`proxy_type` enum
`http|https|socks5|socks5h`, `proxy_url`, `timeout` duration, `solver_type` enum
`manual_cookie|flaresolverr`, `flaresolverr_url`) as named optional settings; add a machine-readable
`code` to the error schema (and document the redaction/merge/test semantics that today live only in
prose). **Do not touch OIDC** — the `/api/auth/oidc/login`+`/callback` stubs stay mounted-but-`501` and
documented (already drift-test-green); resolving that honesty gap is deferred entirely to Phase 10, where
OIDC is actually implemented (`docs/prompts/phase10.md`).

---

## HARD RULES (do not work around)

- **Redaction is absolute.** No passkey, download-link, Bearer token, or stored secret in a JSON search
  response, error, or log. A resolver-needing indexer's `link` is `/dl`-tokenized exactly like the feed;
  the change-password endpoint never logs either password. **Assert redaction with a test** (a
  resolver-needing stub indexer whose raw link carries a recognizable key must yield a `/dl` link in the
  JSON, with the key absent).
- **Reuse the engine; never fork Torznab.** The JSON endpoints call `registry.Indexer(...).Search/
  .Capabilities` and the shared `buildQuery`; the JSON search returns the **same releases** the Torznab
  `t=search` does for the same query (a parity test pins this). Do not duplicate or special-case the
  Torznab handler, and do not invent a parallel release type — marshal `normalizer.Release`.
- **The drift test stays green** — document every new route in `openapi.yaml` in the **same commit**.
  (Leave the OIDC stubs as-is — they're already mounted + documented; OIDC is deferred to Phase 10.)
- **Two contracts stay separate** — this phase adds JSON management endpoints; it does not change the
  Torznab XML feed's bytes or behavior.
- **Typed DTOs** (no `map[string]any` for request/response), gofumpt-clean, split god-functions
  (funlen/gocyclo), SQLite-only, conventional commits, **NO AI attribution / co-author / "Generated
  with" lines**.
- **Branch & box.** One PR off `main` on `phase8b/management-api`; NEVER touch `main`.

---

## ORACLE / TESTS (deterministic; the gate)

- **Parity** — for representative queries, the JSON `/search` returns the same releases (count + identity)
  as the Torznab `t=search` path, because both use the same `buildQuery` + `idx.Search`. Assert against a
  stub indexer that returns a fixed release set.
- **Redaction** — a resolver-needing stub indexer (raw `link` carrying a recognizable key) yields a `/dl`
  link in the JSON search response; the key/secret appears in no body, error, or log.
- **Per-endpoint handler tests** (table-driven, `httptest` + cookie jar / `X-API-Key`): search (params →
  query mapping, JSON shape, `404`/`500`); capabilities (modes/categories JSON); definition-detail
  (ordered settings + `secret` flags + caps, id-validation/traversal rejection); change-password
  (current-password required + verified, hash rotated, `400`/`401`).
- **Drift test green** + the new operations render in `/api/docs` (and have examples). An integration test
  exercises 1–2 error paths to assert the new error `code` field.

---

## WORK LIST — items in dependency order (one PR)

1. **Shared query mapping + router wiring.** Extract/reuse `buildQuery` (+ `parsePaging`) into a shared
   package both contracts call, so the JSON search and the Torznab feed map params identically; wire the
   keyring/dlToken + base path into the management router/handler so a handler can `/dl`-tokenize a
   resolver link. *(enabling — ticks nothing.)*
2. **`GET /api/indexers/{slug}/search`.** Params → `search.Query` → `idx.Search` → JSON releases, resolver
   links `/dl`-tokenized; spec + parity test + redaction test + status handling.
3. **`GET /api/indexers/{slug}/capabilities`.** `Capabilities()` → JSON (modes/params/categories/limits);
   spec + test.
4. **`GET /api/definitions/{id}`.** `loader.Load` (+ native catalog) + `mapper.Build` → settings-field
   schema (with `secret` flags) + caps; id-validation; spec + test.
5. **`POST /api/auth/change-password`.** Verify current password + `UpdatePassword` + session renewal;
   spec + test.
6. **Spec hardening.** Document the config knobs (proxy/timeout/solver/reserved secrets) with enums; add
   the error `code`; add examples. *(no new routes; updates the spec the drift test guards — keep it green.
   OIDC is NOT touched — deferred to Phase 10.)*

---

## RISKS (carry into the plan with concrete tests)

- **Passkey leak in the JSON search** (the #1 risk) — a resolver-needing indexer's raw `link` reaching the
  JSON body. Mitigate by `/dl`-tokenizing exactly like the feed; test it with a key-bearing stub.
- **Parity drift** — reimplementing param mapping or paging diverges from the feed. Reuse `buildQuery` +
  `parsePaging`; assert JSON `≡` Torznab for the same query.
- **Drift-test breakage** — a new route left undocumented fails `TestOpenAPIDriftRoutesMatchSpec`.
  Document every new route in the same commit. (The OIDC stubs already pass — don't disturb them.)
- **definition-detail secret exposure** — this endpoint is the DEFINITION schema (cleartext labels/defaults
  + a `secret` flag), NOT an instance's settings; never echo a configured instance's stored secret here.
- **change-password bypass** — require AND verify the current password (reuse the login verifier); never
  log either value; renew the session.
- **Health classification** — a JSON-search failure should record the same health event a feed search
  does (reuse the adapter's `Search`, which already records it — don't bypass it).

## SUCCESS CRITERIA — assert as a gate

- The four endpoints implemented, documented, and drift-test-green; JSON search `≡` Torznab search
  (parity proven); **no passkey/secret in any JSON response/error/log** (redaction proven).
- Spec hardened: config knobs documented with enums, error `code` added. (OIDC untouched — deferred to Phase 10.)
- `make precommit` + `make build` green (`-race`); all cross-builds green; contracts stay separate;
  SQLite-only; PR ≤150 files.

## PER-ITEM LOOP (after plan approval; one commit per item)

For each WORK LIST item: **(a)** brief per-item plan · **(b)** implement + table-driven tests (httptest;
parity + redaction for search; per-endpoint handler tests) · **(c)** verify `make precommit` + `make
build` (`-race`) + the drift test green · **(d) ≥3 adversarial skeptics** target: a passkey/secret
leaking into the JSON search; JSON-vs-Torznab result divergence; an undocumented route breaking the drift
test; definition-detail exposing a configured secret; the change-password current-password check being
bypassable. Fix every confirmed issue; re-verify. (Fall back to rigorous inline self-review if skeptic
agents die on spend, and say so.) · **(e)** one focused conventional commit.

## AFTER ALL ITEMS

**(f)** End-to-end review + completeness critic (is the documented API now complete for the control +
data plane? did the spec stay accurate?). Add the new endpoints to `docs/highlights.md` (`[shipped]`);
record any divergence with a disposition and add ONE row to `docs/divergences.md`'s table if warranted.
**(g)** keep the PR ≤150 files. **(h)** open the PR → `main`; summary + testing checklist + the endpoint
table; **no creds/tokens/tracker URLs in the body**; no AI attribution. **(i)** push, CI green. **(j)**
address every CodeRabbit finding (validate → fix → revalidate). **(k) PAUSE** — once CI + review are
green, STOP; do NOT merge; wait for approval.

## FINAL REPORT

State the items shipped (commit ids); the four endpoints + their contracts as built; the parity proof
(JSON search `≡` Torznab) and the redaction proof (no passkey/secret in a JSON response); the
spec-hardening done (config knobs, error `code`; OIDC left untouched, deferred to Phase 10); explicit confirmation that no
passkey/download-key/Bearer/stored-secret appears in a JSON response, error, log, or commit; the
out-of-scope items left deferred (with the issue's reasons); and which required checks ran.
