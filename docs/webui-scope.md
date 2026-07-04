# Phase 12 — Web UI scope

Expands the one-line Phase 12 entry in `docs/plan.md`. The stack question flagged there is resolved:
qui was verified during scoping and harbrr mirrors it exactly (see §6). Visual direction is locked by
the approved mockup (claude.ai design project "harbrr web UI", `Indexers.dc.html`).

## 1. Goal and non-goals

**Goal:** a single-page management dashboard, embedded in the harbrr binary, that covers the full
operator surface of the management API — indexer lifecycle, schema-driven add/edit forms, manual
search with grab, app-sync/announce management, and settings/stats — so an operator never needs
Swagger UI for day-to-day work. Swagger UI stays at `/api/docs` (the SPA links to it).

**v1 non-goals (fixed):** i18n; PWA/offline; virtualized tables (page sizes ≤100); multi-user/roles;
mobile-first (must merely not break on tablet); websockets/SSE (poll with React Query
`refetchInterval` instead); OIDC (backend is a 501 stub — hide it); Jackett/Prowlarr migration import
(backlog Tier 1); send-to-download-client (`internal/download` is a stub, autobrr/harbrr#8).

harbrr is single-user self-hosted software: prefer simple readable code over enterprise patterns.

## 2. Screen inventory

### Setup (first-run wizard) — `/setup`
- **Purpose:** create the single admin account on first boot.
- **Components:** centered card, username + password + confirm fields, submit.
- **Endpoints:** `GET /api/auth/setup` (gate), `POST /api/auth/setup` → redirect to `/login`.

### Login — `/login`
- **Purpose:** establish the session cookie.
- **Components:** centered card, username/password form, error banner on `invalid_credentials`.
- **Endpoints:** `POST /api/auth/login` (204 sets `harbrr_session` + `harbrr_csrf`), then
  `GET /api/auth/me` to hydrate identity + CSRF token.

### Dashboard — `/` (default screen)
- **Purpose:** at-a-glance value + health — is harbrr healthy, and how much tracker traffic it is
  saving. *(Approved addition beyond the mockup, decided 2026-07-03.)*
- **Components:** stat tiles (indexers configured/healthy, `trackerHitsSaved` headline + hit ratio,
  app connections, breaker open count); per-indexer health strip (reuses the Indexers health cell +
  its query keys); quick links (Add indexer, Search).
- **Endpoints:** `GET /api/indexers`, per-slug `GET /api/indexers/{slug}/status` (shared React
  Query keys with the Indexers screen — no duplicate fetches), `GET /api/cache/stats`,
  `GET /api/app-connections`, `GET /healthz`.

### Indexers — `/indexers`
- **Purpose:** the mockup screen — list configured instances, health at a glance, lifecycle actions.
- **Components:** header (title, "N configured · N healthy" subtitle, filter input, primary
  "Add indexer" button); card-wrapped table with columns **Indexer** (colored-initial avatar + name +
  host), **Type** (Private/Public/Semi-private pill — joined client-side from `GET /api/definitions`
  `type`), **Categories** (compact summary from capabilities), **Health** (pulsing dot +
  Healthy/Degraded/Error + last-checked), **Enabled** (switch), **Actions** (Test button + kebab:
  Edit, Cross-seed snippet, Copy feed URL, Delete); "Showing X of N" footer; detail drawer per row
  (status events, per-indexer stats, capabilities).
- **Endpoints:** `GET /api/indexers`; per-row `GET /api/indexers/{slug}/status` (health — no bulk
  status endpoint exists, so a `useQueries` fan-out; fine at single-user scale) and
  `GET /api/indexers/{slug}/capabilities` (categories, lazily); `POST .../enable` / `.../disable`;
  `POST .../test`; `DELETE /api/indexers/{slug}`; drawer adds `GET .../stats`;
  `GET .../crossseed-snippet` (copy dialog).

### Add / Edit indexer — sheet on `/indexers` (not a route)
- **Purpose:** schema-driven dynamic form; one component serves create and edit.
- **Add flow:** searchable definition picker (`GET /api/definitions` → id/name/description/type/
  language) → on pick, `GET /api/definitions/{id}` returns the ordered `settings: [SettingField]`
  array (`{name, label?, type: text|password|checkbox|select|multi-select|info*, default?, options,
  secret}`) — render fields **in array order**; `secret: true` renders a masked input; `info*` types
  render as read-only callouts; `caps` previews categories/modes. Plus name/slug/baseUrl inputs and a
  collapsed **Advanced** section hand-built from the spec's `ReservedSettings` list (`proxy_type`,
  `proxy_url` (secret), `timeout`, `solver_type`, `flaresolverr_url`, `flaresolverr_max_timeout`,
  `cookie` (secret)) — no schema endpoint serves these per definition. Submit →
  `POST /api/indexers`, 409 → inline slug-conflict error.
- **Edit flow:** `GET /api/indexers/{slug}` prefills; secret fields arrive as the literal
  `<redacted>` sentinel and stay prefilled with it — **PATCHing `<redacted>` back keeps the stored
  secret**; only user-typed values rotate it. Submit → `PATCH /api/indexers/{slug}` (merge
  semantics), then `POST .../test` offered as a follow-up.

### Search — `/search`
- **Purpose:** manual search — the JSON twin of the Torznab feed, same pipeline.
- **Components:** query input; indexer multi-select (enabled instances); category picker (union of
  the selected indexers' capability trees); optional id-search inputs (imdbid/tmdbid/tvdbid,
  season/ep) gated on the selected indexers' modes; results table (title, indexer, category, size,
  seeders/leechers, age, grab button) with client-side sort (add `@tanstack/react-table` here only if
  plain sorting isn't enough); limit/offset pager (`hasMore`).
- **Endpoints:** `GET /api/indexers/{slug}/search` fanned out per selected indexer, results merged
  client-side; `GET /api/indexers/{slug}/capabilities` for pickers. **Grab:** render the
  API-returned, `/dl`-token-sealed download link **verbatim as an anchor href** (browser fetches the
  `.torrent`; magnets 302). The UI never constructs feed/dl URLs and never logs result URLs
  (passkeys may legitimately appear in magnet links).

### Applications — `/applications`
- **Purpose:** app-sync targets (*arr/qui) and cross-seed announce targets.
- **Components:** two sections (tabs or stacked cards). **App connections:** card list showing kind,
  baseUrl, enabled switch, per-app **freeleech mode** (honor/bypass) and sync level/scope badges,
  last-sync status + error; actions: Test, Sync now, Edit (dialog), Delete; header "Sync all" button;
  status drawer with the per-indexer sync ledger; "Select indexers" dialog when
  `indexScope=selected`. **Announce targets:** card list (kind qui|crossseed-v6), enabled switch,
  Add dialog, Delete — **no edit and no test** (API has no PATCH/test for announce; editing is
  delete + recreate — the UI says so inline). A cross-seed v6 helper links each indexer's
  `crossseed-snippet` (cross-seed has no API; the operator pastes `configJs`).
- **Endpoints:** `GET/POST /api/app-connections`, `GET/PATCH/DELETE /api/app-connections/{id}`,
  `POST .../enable|disable|test|sync`, `POST /api/app-connections/sync`, `GET .../status`,
  `PUT .../indexers`; `GET/POST /api/announce-connections`,
  `DELETE /api/announce-connections/{id}`, `POST .../enable|disable`.

### Settings — `/settings` (sectioned page: Cache, API keys, Notifications, Logging, Account, About)
- **Purpose:** runtime knobs, stats, credentials, identity.
- **Cache:** stats card with a **`trackerHitsSaved` headline stat**, hit ratio, entries, size, and
  the `byIndexer` breakdown incl. circuit-breaker state (`breakerOpenUntil`); "Flush cache" button
  (confirm dialog, shows `{flushed: n}`); config form for the live-tunable knobs (`rssTtl`,
  `keywordTtl`, `thinTtl`, `thinThreshold`, `refreshAheadPct`, `negativeTtl`, `cleanupInterval` —
  Go duration strings, zod-validated). Disabled cache renders an "enabled: false" empty state.
- **API keys:** list (name, created, last used), mint dialog → **one-time copy dialog** for the
  plaintext `key` (never refetchable), revoke.
- **Notifications:** CRUD list (type webhook|discord, `onHealthFailure` toggle, enabled switch,
  Test button); `url` is a stored secret — same `<redacted>` round-trip as indexer settings.
- **Logging:** current level select → `PUT /api/config/log-level` (applies live).
- **Account:** change-password form (hidden when `authMethod` ≠ `password`).
- **About:** version/commit from `GET /healthz`, health dot, links to `/api/docs` (Swagger UI) and
  `/api/openapi.yaml`, per-indexer stats table from `GET /api/indexers/stats`.
- **Endpoints:** `GET /api/cache/stats`, `POST /api/cache/flush`, `GET/PUT /api/cache/config`;
  `GET/POST /api/apikeys`, `DELETE /api/apikeys/{id}`; `GET/POST /api/notifications`,
  `GET/PATCH/DELETE /api/notifications/{id}`, `POST .../enable|disable|test`;
  `GET/PUT /api/config/log-level`; `POST /api/auth/change-password`; `GET /healthz`;
  `GET /api/indexers/stats`.

## 3. Route tree and layout

qui-style file-based TanStack Router routes; `routeTree.gen.ts` generated by `tsr generate`
(defaults, no config file) and **committed**:

```
web/src/routes/
├── __root.tsx              # bare <Outlet/> + notFoundComponent
├── login.tsx
├── setup.tsx
├── _authenticated.tsx      # auth guard (useAuth): loading → null, unauthed → <Navigate to="/login"/>, else <AppLayout/>
└── _authenticated/
    ├── index.tsx           # Dashboard (default screen at /)
    ├── indexers.tsx
    ├── search.tsx
    ├── applications.tsx
    └── settings.tsx
```

**Layout (`layouts/AppLayout.tsx`), matching the mockup shell:** 240px left sidebar — harbrr logo +
version chip; **Dashboard** on top (approved addition beyond the mockup), then nav groups **Manage**
(Indexers with configured-count badge, Search) and **Sync** (Applications with connection-count
badge); footer: Settings link, signed-in user chip (from `auth/me`; hosts logout), Light/Dark/System
theme control. Content area renders the `<Outlet/>`.
Router is created with `basepath` from `window.__HARBRR_BASE_URL__` (injected at serve time, §7).

## 4. Endpoint wiring map

Every management-router operation, plus the out-of-spec mounts. "Screen → component" names where the
call originates; all calls go through the single API client (§6).

| Endpoint | UI wiring |
|---|---|
| `GET /healthz` | Settings → About (version/commit/health); sidebar version chip; Dashboard health tile |
| `GET /api/config/log-level` | Settings → Logging |
| `PUT /api/config/log-level` | Settings → Logging |
| `GET /api/auth/setup` | App bootstrap: on 401 from `me`, routes to `/setup` vs `/login` |
| `POST /api/auth/setup` | Setup wizard |
| `POST /api/auth/login` | Login screen |
| `POST /api/auth/logout` | Sidebar user chip → Logout |
| `GET /api/auth/me` | App bootstrap + auth guard; CSRF token source; `authMethod` branch |
| `POST /api/auth/change-password` | Settings → Account |
| `GET /api/auth/oidc/login` | **Not in UI v1** — backend 501 stub; SSO hidden entirely |
| `GET /api/auth/oidc/callback` | **Not in UI v1** — backend 501 stub |
| `GET /api/apikeys` | Settings → API keys list |
| `POST /api/apikeys` | Settings → API keys mint + one-time copy dialog |
| `DELETE /api/apikeys/{id}` | Settings → API keys revoke |
| `GET /api/definitions` | Add-indexer picker; Indexers table Type pill (client-side join) |
| `GET /api/definitions/{id}` | Add-indexer dynamic form schema + caps preview |
| `GET /api/indexers` | Indexers table; Search indexer multi-select; sidebar count badge; Dashboard tiles + health strip |
| `POST /api/indexers` | Add-indexer form submit |
| `GET /api/indexers/stats` | Settings → About per-indexer stats table |
| `GET /api/indexers/{slug}` | Edit-indexer form prefill (redacted settings) |
| `PATCH /api/indexers/{slug}` | Edit-indexer form submit (`<redacted>` round-trip) |
| `DELETE /api/indexers/{slug}` | Indexers row kebab → Delete (confirm) |
| `POST /api/indexers/{slug}/enable` | Indexers row Enabled switch |
| `POST /api/indexers/{slug}/disable` | Indexers row Enabled switch |
| `POST /api/indexers/{slug}/test` | Indexers row Test button; post-save test in add/edit sheet |
| `GET /api/indexers/{slug}/status` | Indexers Health column (per-row fan-out) + detail drawer events; Dashboard health strip (shared query keys) |
| `GET /api/indexers/{slug}/stats` | Indexers detail drawer |
| `GET /api/indexers/{slug}/search` | Search screen (fan-out per selected indexer) |
| `GET /api/indexers/{slug}/capabilities` | Search category/mode pickers; Indexers Categories column + drawer |
| `GET /api/indexers/{slug}/crossseed-snippet` | Indexers row kebab → Cross-seed snippet dialog; Applications helper |
| `GET /api/app-connections` | Applications list; sidebar count badge; Dashboard tile |
| `POST /api/app-connections` | Applications → Add connection dialog |
| `POST /api/app-connections/sync` | Applications header "Sync all" |
| `GET /api/app-connections/{id}` | Edit-connection dialog prefill |
| `PATCH /api/app-connections/{id}` | Edit-connection dialog submit (new `apiKey` rotates) |
| `DELETE /api/app-connections/{id}` | Connection card → Delete (confirm) |
| `POST /api/app-connections/{id}/enable` | Connection card enabled switch |
| `POST /api/app-connections/{id}/disable` | Connection card enabled switch |
| `POST /api/app-connections/{id}/test` | Connection card Test button |
| `POST /api/app-connections/{id}/sync` | Connection card "Sync now" |
| `GET /api/app-connections/{id}/status` | Connection status drawer (per-indexer sync ledger) |
| `PUT /api/app-connections/{id}/indexers` | "Select indexers" dialog (when `indexScope=selected`) |
| `GET /api/announce-connections` | Applications → Announce targets list |
| `POST /api/announce-connections` | Announce → Add dialog |
| `GET /api/announce-connections/{id}` | **Not in UI v1** — list payload already carries every field and there is no edit form (API has no PATCH; edit = delete + recreate) |
| `DELETE /api/announce-connections/{id}` | Announce card → Delete (confirm) |
| `POST /api/announce-connections/{id}/enable` | Announce card enabled switch |
| `POST /api/announce-connections/{id}/disable` | Announce card enabled switch |
| `GET /api/notifications` | Settings → Notifications list |
| `POST /api/notifications` | Notifications → Add dialog |
| `GET /api/notifications/{id}` | Edit-notification dialog prefill |
| `PATCH /api/notifications/{id}` | Edit-notification dialog submit |
| `DELETE /api/notifications/{id}` | Notification row → Delete |
| `POST /api/notifications/{id}/enable` | Notification row enabled switch |
| `POST /api/notifications/{id}/disable` | Notification row enabled switch |
| `POST /api/notifications/{id}/test` | Notification row Test button |
| `GET /api/cache/stats` | Settings → Cache stats card (`trackerHitsSaved` headline, `byIndexer`); Dashboard headline tile |
| `POST /api/cache/flush` | Settings → Cache flush button (confirm) |
| `GET /api/cache/config` | Settings → Cache config form prefill |
| `PUT /api/cache/config` | Settings → Cache config form submit |
| `GET .../results/torznab` (+ `/api` alias) | **Not called by UI** — machine-facing feed (*arr/autobrr/qui); UI only surfaces its URL (copy action) |
| `GET .../results/torznab/full` (+ `/api` alias) | **Not called by UI** — machine-facing (cross-seed consumers); URL surfaced via crossseed-snippet |
| `GET /api/indexers/{slug}/dl` | **Not called via API client** — grab links returned by JSON search are rendered verbatim as anchor hrefs (browser fetch/302) |
| `GET /api/openapi.yaml` | **Not called** — linked from Settings → About |
| `GET /api/docs` | **Not called** — linked from Settings → About and sidebar footer |

No other management endpoints exist (65 operations / 50 path items, drift-tested via
`make test-openapi`); every one is accounted for above.

## 5. SPA auth flow

- **Bootstrap:** on load, `GET /api/auth/me`. 200 → authed (hydrate `{username, authMethod,
  csrfToken}`). 401 → `GET /api/auth/setup`; `setupComplete:false` → `/setup`, else `/login`.
- **Session:** cookie `harbrr_session`, HttpOnly — the SPA never reads it; all fetches are
  same-origin with credentials. No tokens in JS storage.
- **CSRF:** double-submit token, required on **cookie-authenticated mutating** requests
  (POST/PUT/PATCH/DELETE). Acquire from the non-HttpOnly `harbrr_csrf` cookie, falling back to
  `me.csrfToken` on reload; inject as **`X-CSRF-Token`** at the API client's single `request()`
  choke point. 403 with CSRF error → re-fetch `me` once and retry, else surface. Token re-mints on
  login; `change-password` renews the session token (no forced re-login).
- **401 handling:** the API client hard-redirects to `/login` (base-path aware) on 401 — **except**
  for `/api/auth/me` and `/api/auth/setup` responses and when already on `/login`//`setup`
  (prevents redirect loops). React Query cache is cleared on logout.
- **Auth-disabled mode** (`auth.mode=disabled` + IP allowlist): `me` returns
  `{username:"admin", authMethod:"disabled", csrfToken:""}` — the SPA branches on `authMethod`:
  hide login/logout/change-password, omit the CSRF header (empty token; middleware exempts it).
- **Errors:** the API returns uniform `{error, code}` — the client raises a typed `APIError`
  carrying `status` + `code` so screens can branch (e.g. `conflict` → inline slug error).

## 6. Frontend architecture (mirrors qui)

**Stack (fixed):** pnpm · Vite · React 19 · TypeScript · Tailwind CSS v4 (`@tailwindcss/vite`,
CSS-first — **no** `tailwind.config.*`) · shadcn/ui (Radix + CVA + tailwind-merge, neutral base) ·
TanStack Router/Query/Table/Form · zod · lucide-react · sonner · next-themes.

```
web/
├── build.go                # package web; //go:embed all:dist; DistDirFS strips "dist/"
├── dist/.gitkeep           # committed so go build never fails without a frontend build
├── package.json  vite.config.ts  components.json  index.html  tsconfig*.json
└── src/
    ├── main.tsx  App.tsx  router.tsx  routeTree.gen.ts  index.css
    ├── routes/             # §3
    ├── layouts/AppLayout.tsx
    ├── lib/                # api.ts, base-url.ts, utils.ts, validation schemas
    ├── hooks/              # useAuth, useIndexers, useDefinitions, useSearch, useAppConnections, …
    ├── components/{ui,layout,indexers,search,applications,settings}/
    ├── types/              # hand-written API types matching openapi.yaml components
    └── pages/              # NotFound
```

- **API client** (`lib/api.ts`): one `ApiClient` class, singleton `api`, one typed method per
  endpoint; private `request<T>(endpoint, options)` prefixes `getApiBaseUrl()`
  (`window.__HARBRR_BASE_URL__` + `/api`), sends same-origin credentials, injects `X-CSRF-Token` on
  mutations, parses `{error, code}` into `APIError{status, code}`, handles 401 per §5. Skip qui's
  ~250-line SSO-recovery layer. **Never** console.log payloads containing `settings` or URLs from
  search results (client-side mirror of the server's redaction posture).
- **React Query:** hooks per domain in `src/hooks/`. Array-literal keys:
  `["auth","me"]`, `["definitions"]`, `["definitions", id]`, `["indexers"]`, `["indexers","stats"]`,
  `["indexers", slug]`, `["indexers", slug, "status"|"stats"|"capabilities"]`,
  `["search", slug, params]`, `["app-connections"]`, `["app-connections", id, "status"]`,
  `["announce-connections"]`, `["notifications"]`, `["apikeys"]`, `["cache","stats"]`,
  `["cache","config"]`, `["config","log-level"]`, `["health"]`. Mutations invalidate their resource
  root key in `onSettled` (optimistic updates only for the enable/disable switches, qui's
  `useInstances` pattern). Defaults: `staleTime: 5s`, `refetchOnWindowFocus: false`; health/status
  queries poll via `refetchInterval` (30s) — no SSE.
- **Forms:** TanStack Form; `defaultValues` computed from an optional entity prop so one component
  serves create + edit; zod schemas called imperatively inside per-field `validators.onChange`
  (no resolver layer); submit massages the payload — **fields still equal to `<redacted>` are sent
  back as-is on PATCH (keep-stored contract) and stripped from POSTs**; sonner toasts on
  success/error; `formId` prop so submit buttons can live in dialog/sheet footers.
- **Theming:** all tokens in `src/index.css` — `:root {…}` / `.dark {…}` blocks carrying the
  mockup's OKLCH palette (neutrals hue 285: bg `oklch(0.205 …)` / surface 0.235 / raised 0.27 /
  border 0.32; brand `oklch(0.68 0.16 250)`; ok `oklch(0.74 0.16 155)`; warn `oklch(0.80 0.15 85)`;
  bad `oklch(0.66 0.20 25)`; 13px base UI type), mapped to utilities via a `@theme inline {}` block,
  `@custom-variant dark (&:is(.dark *))`. Theme state via plain **next-themes** (class attribute,
  `enableSystem`; a **Light / Dark / System** control in the sidebar footer, default **System** —
  decided 2026-07-03; the mockup's dark palette is the `.dark` block, the light palette derived
  from the same hues) — not qui's multi-theme engine. Icons: lucide stroke style.
- **Explicitly skipped from qui** (per scoping): i18n, PWA/service worker, virtualized tables +
  persisted-column hooks, SSE sync context, premium-theme engine, dnd-kit, parse-torrent + node
  polyfills, SSO-recovery fetch layer, motion/extras.

## 7. Build and integration

- **Embed:** `web/build.go` — `//go:embed all:dist` + `DistDirFS`; `web/dist/.gitkeep` committed.
- **Serving:** new `internal/web/ui` package modeled on qui's `internal/web/handler.go` minus PWA:
  `/assets/*` + enumerated root assets with correct MIME + `Cache-Control: immutable` for hashed
  files; everything else serves `index.html` (SPA fallback for deep links), injecting
  `<script>window.__HARBRR_BASE_URL__=…;window.__HARBRR_VERSION__=…</script>` before `</head>`
  (same substitution pattern as `internal/web/swagger`). When `dist` holds only `.gitkeep`, return
  404 "Frontend not built — run `make web-build`".
- **Mount** (`internal/server/server.go`): re-shape the root router — `GET /healthz` and
  `Handle("/api/*", management)` claim the API namespace explicitly; feed + `/api/openapi.yaml` +
  `/api/docs` mounts stay as-is (registered first); the SPA takes the **`/*` catch-all**. Zero
  collisions by construction; `server.base_url` continues to work via the existing outer
  `http.StripPrefix`. Asset paths mirror qui exactly: Vite keeps its default **absolute** `base`
  (no `base: './'` — relative paths would break deep-link fallback: `/indexers` would resolve
  assets to `/indexers/assets/…`), and when `base_url` is set the `ui` handler rewrites
  `src="/assets/…"` / `href="/assets/…"` in the served `index.html` at serve time, the same
  mechanism as qui's `internal/web/handler.go`; the SPA reads the injected base for router
  `basepath` + API base. Handler passed via `server.Deps` like `DocsUI` today.
- **Dev loop:** `pnpm dev` with Vite proxy `server.proxy["/api"] → http://localhost:7478`
  (harbrr's default listen) — run `./bin/harbrr` alongside.
- **Makefile:** `web-deps` (pnpm install), `web-build` (install + `pnpm build` → `web/dist`),
  `web-dev` (`pnpm dev`), `web-test` (vitest), `web-lint`. `make build` stays Go-only and embeds
  whatever is in `web/dist` (gitkeep-only OK for dev); **release builds run `web-build` first and
  hard-fail if `web/dist` contains only `.gitkeep`** (decided 2026-07-03 — every shipped
  binary/image must contain the UI).
- **CI:** one new `web` job (pnpm frozen install → lint → vitest → `pnpm build`) running **in
  parallel with the Go test/lint jobs**; it builds the frontend **exactly once** and uploads
  `web/dist` as an artifact. Every binary-producing job — `cross-build` (all targets), `docker`
  (both arches), and (on tag runs) `goreleaser`, all inside `ci.yml`; there is no standalone
  release workflow — downloads that artifact instead of rebuilding, so every shipped binary
  embeds the identical tested bundle, **guarded by `make check-web-dist`, which hard-fails when
  `web/dist` lacks a real build** (decided 2026-07-03) — otherwise a shipped binary/image would
  embed an empty `web/dist` and serve the "Frontend not built" 404. `make web-ci` mirrors the
  web job's exact steps locally.
- **OpenAPI drift test:** `make test-openapi` compares the spec to the *management router's* mounted
  routes — the SPA adds no management endpoints, so it is unaffected. The `server.go` re-shape
  (`/*` → `/api/*` for the management mount) must keep it and all `internal/server` tests green —
  that's part of the walking-skeleton gate.

## 8. Build order (checkbox leaves, plan.md style)

Each leaf lands with its test gate green before the next starts. In addition to the per-leaf gates
below, **every leaf** finishes with `make precommit` + `make build` green (CLAUDE.md requires them
for any code change; they are cheap no-ops when Go is untouched) and, for leaves touching `web/`,
`pnpm lint && pnpm test && pnpm build`.

**PR cadence (decided 2026-07-03): three sequential PRs, each merged before the next branch cuts —
NOT one mega-PR and NOT unmerged stacked chains.** CodeRabbit auto-skips any PR over 150 changed
files and allows ~1 review/hour, so batch to three: **PR 1** = leaves 1–3 (skeleton + shell/theme +
auth), **PR 2** = leaves 4–5 (Indexers + add/edit form), **PR 3** = leaves 6–10 (Search,
Applications, Settings, Dashboard, polish). Keep every PR ≤150 changed files — if PR 3 threatens
the cap, split Settings+Dashboard+polish into a fourth. Let CodeRabbit's PR-open auto-review run
(never post `@coderabbitai review` right after opening); push fix commits for incremental
re-review.

- [x] **Scaffold + embed + serve (walking skeleton)** — `web/` scaffold (Vite + React + TS +
      Tailwind v4 + shadcn init + router with a stub index route); `web/build.go` embed;
      `internal/web/ui` handler (SPA fallback + base-URL/version injection + not-built 404);
      `server.go` re-shaped mount; Makefile `web-*` targets; CI node job.
      *Gate:* `make build && ./bin/harbrr` serves the SPA at `/`, a deep link falls back to
      `index.html`, `/healthz` + a feed URL still answer; go tests for the handler (asset MIME,
      fallback, injection, not-built) pass; `make test-openapi` + `make precommit` green.
- [x] **App shell + theme** — `AppLayout` sidebar per mockup (nav groups, version chip, footer),
      OKLCH tokens in `index.css`, next-themes toggle, NotFound page.
      *Gate:* vitest renders `AppLayout` with nav items + theme toggle flips the `dark` class;
      manual: shell matches the mockup in both themes.
- [x] **Auth: login, setup, guard** — `useAuth`, API client with CSRF injection + 401 redirect +
      `APIError`, `/login` + `/setup` routes, `_authenticated` guard, auth-disabled branch, logout.
      *Gate:* vitest: client sets `X-CSRF-Token` on mutations and omits it when token empty;
      manual: full setup → login → logout round-trip against `./bin/harbrr`.
- [x] **Indexers list** — table per mockup (avatar, type pill, categories, health fan-out, enabled
      switch with optimistic toggle, Test, kebab: delete/snippet/feed-URL copy), filter, detail
      drawer (status events + stats + caps).
      *Gate:* vitest renders the table from fixture JSON incl. health states; manual against a live
      binary with ≥2 configured indexers.
- [x] **Add/Edit indexer (dynamic form)** — definition picker, schema-driven `SettingField`
      renderer (every field type incl. `info*`), Advanced/ReservedSettings section, `<redacted>`
      round-trip, 409 slug handling, post-save test.
      *Gate:* vitest renders each `SettingField` type from a fixture schema and asserts a PATCH
      payload preserves `<redacted>` for untouched secrets; manual add of a real definition.
- [x] **Search + grab** — query form, indexer multi-select, capability-driven category/mode pickers,
      merged results table with sort + pager, verbatim grab links.
      *Gate:* manual: a query against a live configured indexer returns rows and the grab link
      downloads a `.torrent`; vitest for the result-row rendering (size/age formatting).
- [x] **Applications (sync + announce)** — app-connection CRUD dialogs, test/sync/sync-all, status
      ledger drawer, selected-indexers dialog, freeleech-mode control; announce-target list with
      add/delete/toggle and the "edit = delete + recreate" notice; cross-seed snippet helper.
      *Gate:* vitest renders a `SyncReport` fixture (ok/partial/error, per-slug actions); manual
      sync against a live *arr or qui instance.
- [x] **Settings + stats** — cache stats card (`trackerHitsSaved` headline, `byIndexer`, breaker
      state) + flush + config form; API keys with one-time copy dialog; notifications CRUD + test;
      log level; change password; About (version, health, per-indexer stats, docs links).
      *Gate:* vitest: mint dialog shows the key exactly once and never re-renders it; manual cache
      `PUT` round-trip + flush against the live binary.
- [x] **Dashboard** — stat tiles (indexers configured/healthy, `trackerHitsSaved` + hit ratio, app
      connections, breaker open count), per-indexer health strip (shared query keys with the
      Indexers screen), quick links; `_authenticated/index.tsx` default route at `/`.
      *Gate:* vitest renders the tiles from stats/status fixtures; manual against the live binary
      with the cache warm.
- [x] **Polish pass** — empty states for every list (zero indexers, cache disabled, no
      connections), error toasts + `APIError.code` branching, loading skeletons, tablet-width
      no-break check, secret-hygiene sweep (no payload logging, masked fields everywhere).
      *Gate:* manual pass over every screen with (a) an empty database and (b) the server stopped
      mid-session; `make precommit` + full vitest suite green.

## 9. Resolved decisions (operator, 2026-07-03)

1. **Dashboard: in v1.** `/` is a Dashboard screen (§2) — stat tiles + per-indexer health strip;
   Indexers moves to `/indexers` only.
2. **Announce editing: delete + recreate accepted for v1.** No `PATCH`/`test` endpoints added; the
   UI shows an "edit = delete + recreate" notice.
3. **Grab UX: browser `.torrent` download only.** The verbatim `/dl` anchor link is the whole grab
   story; send-to-download-client (autobrr/harbrr#8) stays out of this phase.
4. **Release packaging: hard-fail on empty `web/dist`.** `make build` stays Go-only for dev;
   release-artifact jobs (`goreleaser`, `docker`) build the frontend and fail the release if
   `web/dist` holds only `.gitkeep` (§7).
5. **Theme: Light / Dark / System control, default System.** Mockup's dark palette is `.dark`;
   light palette derived from the same hues (§6).
6. **PR strategy: three sequential PRs** merged in order (§8) — no mega-PR (CodeRabbit skips >150
   files), no unmerged stacked chains.
