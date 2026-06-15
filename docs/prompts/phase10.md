# Phase 10 implementation prompt — Web UI (management dashboard)

Paste the block below into an `ultracode` session to implement the **Web UI** workstream of Phase 10. It
begins in **plan mode**: the agent must plan the *entire* work stream and get the plan approved before
writing any code. This is the FIRST non-Go (TypeScript) code to enter the repo, so the strict-CI gate
is itself a planned, committed deliverable — not an afterthought.

---

ultracode — Implement **Phase 10 (Web UI / management dashboard)** from `docs/plan.md` as ONE reviewable
PR. **Begin in PLAN MODE — do STEP 0 before anything else.**

This PR ships the **Web UI box** of Phase 10 and the items tightly coupled to it (Swagger UI render,
stats/search-history display, `api_keys.last_used_at` write). OIDC login is **deferred by default** (the
UI hides the 501 `/api/auth/oidc/*` stubs) unless the approved plan promotes it to a work-list item —
see the WORK LIST. The other Phase 10 boxes (\*arr app-sync, Prowlarr import, autobrr push, cross-seed,
notifications, Postgres) are **out of scope** — see the deferral list in the WORK LIST. The web UI does **not** unlock the
"Prowlarr replacement" framing (`docs/ideas.md` §13: that waits until app-sync + migration + UI + broad
coverage all ship — the UI is one of four legs). Do not imply otherwise in any copy, README, or PR body.

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, scaffold a `web/` tree, run
`pnpm create`, or edit files until the plan is approved. Produce ONE complete implementation plan for
the **entire scoped work stream** (the strict-CI foundation **plus** every web-UI work-list item end to
end, not just the first), then present it with ExitPlanMode for my approval and wait. Pressure-test the
plan with a validator/architect agent (and revise) before presenting it.

The plan must cover:
- **Architecture decisions to confirm** (call these out explicitly so I can approve/redirect):
  - **The stack is locked** (operator decision): **React 19 + TypeScript + Vite, qui-aligned** —
    shadcn/ui on Radix + lucide-react, Tailwind v4 (CSS-first via `@tailwindcss/vite`, no JS
    `tailwind.config`), the TanStack quartet (`react-query`, `react-router`, `react-table`,
    `react-form`) + `zod`, Vitest + React Testing Library. **pnpm** package manager, **Node ≥24**.
    Confirm the exact pinned versions you will use against qui (see MODEL AFTER autobrr/qui) and that
    nothing in the toolchain requires cgo or a non-pure-Go build step.
  - **Where `web/` lives + how the SPA is embedded and served** from the single Go binary alongside the
    two existing HTTP contracts (a `web/build.go` with `//go:embed all:dist` exposing `Dist embed.FS` +
    a `DistDirFS` sub-FS, passed as an `fs.FS` into the SPA handler — do NOT embed inside a handler
    package). Confirm the **drift model** (locked below: Model A — commit `web/dist` + `git diff
    --exit-code`).
  - **Build ordering** — frontend before backend (`make build: frontend backend`); the embed must
    compile on a clean checkout WITHOUT Node (committed `web/dist` + a recreated `.gitkeep`).
  - **Auth/session integration** — the SPA relies on the browser sending the HttpOnly `harbrr_session`
    cookie; there is **NO CSRF token** in harbrr (SameSite=Lax is the defence) — confirm the UI sends
    none. First-run flow via `GET /api/auth/setup`.
  - **OIDC** — the two `/api/auth/oidc/*` endpoints return **501** today and there is **NO config seam**
    (only the route stubs + spec entries exist; `AuthConfig` has zero OIDC fields). Decide and state
    whether OIDC ships in THIS PR (net-new config struct + provider client + session integration + UI
    button) or is deferred with a disposition. Default below: **deferred** — see WORK LIST.
  - **Stats/search-history** — the event-log **data layer** (schema + writers + query API) does not
    exist. Decide: does this PR add a minimal read API + the `api_keys.last_used_at` touch-writer so the
    UI has something real to render, or does the UI render only what already exists and defer the
    activity views? State the PR boundary (the data layer is backend; the display is UI).
- **The strict-CI foundation as Commit 1** — the exact toolchain, the `web/`-scoped pre-commit hook,
  the new CI `web` job, the `tsconfig` strictness flags, the Makefile targets, and how it all lands
  **before any feature UI code** (see "STRICT-CI FOUNDATION (Commit 1)" below — the plan must reproduce
  it concretely).
- **Package/file layout** — every file: the `web/` tree (configs + `src/` feature layout + colocated
  `*.test.tsx`), the Go embed glue (`web/build.go`), the SPA HTTP handler + its mount in `internal/web`
  / the server, any `cmd/harbrr serve` router change, `.github/workflows/ci.yml` (+ `lint.yml` if you
  add a JS lint leg there), `.pre-commit-config.yaml`, `Makefile`, `.gitignore`/`.dockerignore`,
  `web/.nvmrc`.
- **Test strategy per item → oracle** — component/unit tests (Vitest + RTL), at least one
  integration test driving the **real management API** offline, accessibility checks, the Swagger-UI
  render assertion, and the embed/drift gate. CI stays **offline and deterministic**.
- **Commit/box sequencing** — Commit 1 is the strict-CI foundation (no Phase 10 box; it is enabling
  infrastructure — say so). Then which `docs/plan.md` Phase 10 box each subsequent commit ticks (the
  **Web UI** box; and, if included, **Swagger UI** as the same box's sub-clause, **OIDC**, and the
  display half of **Stats/search history**). Tick a box only when its tests are green.
- **Risks + mitigations** — the **150-file CodeRabbit cap** (lockfile + many components + assets push a
  UI PR over; plan the split + lockfile/asset hygiene up front), bundle size, two-contract bleed,
  secret display in the UI, the OIDC net-new-config surprise, and the stats data-layer dependency.

After I approve via ExitPlanMode, leave plan mode and execute the PER-ITEM LOOP below.

## READ FIRST

`AGENTS.md`; `docs/plan.md` (Phase 10 — the eight boxes); `docs/ideas.md` **§9 (Security model)** +
**§13 (positioning — no "Prowlarr replacement" claim yet)**; `docs/architecture.md` — **invariant #3**
(the Torznab \*arr-facing contract and the management OpenAPI surface stay SEPARATE — the SPA talks ONLY
to the management API, never the Torznab tree) and **invariant #5** (SQLite only). `docs/divergences.md`
is the divergence-ledger index; `docs/highlights.md` is the honestly-labelled feature log. Read the
management API surface you build against: `internal/web/api/router.go` (the route tree),
`internal/web/api/encode.go` (the `{"error":...}` envelope + status mapping + strict decode),
`internal/web/api/indexer_handlers.go` (the `<redacted>` sentinel on GET, merge-on-PATCH), and
`internal/web/swagger/openapi.yaml` (the spec the UI renders + the contract source of truth).

This PR turns the proven headless daemon into a **usable product**: a browser dashboard that lists,
adds, edits, enables/disables, deletes, and **tests** indexers; runs a **manual search**; shows
indexer **health/status**; mints/revokes API keys; renders the embedded OpenAPI as **Swagger UI**; and
handles first-run setup + login. It is built **entirely against harbrr's real management endpoints** —
do **NOT** invent routes. It does **NOT** touch the engine, the Torznab serializer, or live trackers,
and it does **NOT** ship the independent backend workstreams (app-sync, import, push, cross-seed,
notifications, Postgres).

## MODEL AFTER autobrr/qui

qui is the reference for the **entire** frontend toolchain and strict-CI discipline — harbrr is
"aligned with autobrr/qui" concretely, not loosely. Study qui's `web/` tree at the latest `main` you can
fetch and pin the SHA you inspect (versions drift — re-confirm them then). **Verified against qui `main`
(June 2026)** as this prompt's baseline:
- `web/package.json` — React `^19.2.6`, Vite `^8.0.14`, TypeScript `~6.0.3`, Tailwind `^4.3.0`, the
  TanStack quartet (`@tanstack/react-query ^5`, `react-router ^1`, `react-table ^8`, `react-form ^1`),
  `zod ^4`, `vitest ^4`; `packageManager: pnpm@11.1.2`; `engines.node >=24.0`. **Lint/format is ESLint
  only — no Biome, no Prettier** (`scripts.lint` is `eslint`; `scripts.format` is `eslint --fix`).
  `web/eslint.config.js` (flat config: `eslint ^10` + `typescript-eslint ^8` + `@stylistic/eslint-plugin
  ^5` + `eslint-plugin-react-hooks ^7` + `eslint-plugin-react-refresh ^0.5`; double quotes, 2-space
  `indent` `SwitchCase:1`, multiline `comma-dangle`, `object-curly-spacing: always`, unix linebreaks,
  `@typescript-eslint/no-explicit-any: error`), `web/tsconfig.{json,app,node,test}.json` (solution-style;
  `strict` + the extra opt-ins below).
- `web/build.go` (`//go:embed all:dist` → `Dist embed.FS` + `DistDirFS`), `web/dist/.gitkeep` (empty;
  recreated by the build so the embed never fails on a clean checkout), the SPA handler taking the FS as
  a parameter, the Makefile `build: frontend backend` ordering.
- `web/components.json` (shadcn/ui: style default, lucide icons, `@/` alias), `web/vitest.config.ts`
  (jsdom), the feature-based `web/src` layout with kebab-case filenames and colocated `*.test.tsx`.
- `.github/workflows/lint.yml` + `release.yml` (how qui runs eslint on PRs and `tsc --noEmit` + build +
  vitest in the build path; the Go binary build gated on the frontend job).

Adopt **ESLint flat config** — do NOT introduce Biome or Prettier (qui has neither — verified above).
Formatting is enforced **by ESLint's `@stylistic` rules**, so the read-only gate is simply `eslint .
--max-warnings 0` (no `--fix`): a formatting violation surfaces as an ESLint error and fails the gate.
`eslint --fix` is the developer's local fixer (qui's `format` script) and is NEVER run in the gate — so
there is no separate format tool to add. Match qui's TS strictness exactly: `strict: true` + `noUnusedLocals`, `noUnusedParameters`,
`erasableSyntaxOnly`, `noFallthroughCasesInSwitch`, `noUncheckedSideEffectImports`,
`verbatimModuleSyntax`, `moduleResolution: bundler`, `@/*` alias. qui deliberately does **not** enable
`noUncheckedIndexedAccess` / `noImplicitOverride` / `exactOptionalPropertyTypes` — match that unless you
want harbrr stricter, in which case **call it out** as a labelled decision.

## CONTEXT (Phases 4–7 shipped — the daemon, live, and at scale)

- `harbrr serve` is a real daemon: SQLite + migrations, the §9 secrets store, first-run setup + login +
  `X-API-Key`, the indexer-instance registry as the production `torznab.Provider`, the management API,
  and the Torznab handler at `/api/v2.0/indexers/...`. Phase 5 closed the MVP live; Phases 6–7 added
  operational safety and scale coverage. **None of those are in this PR's scope** — the UI consumes the
  Phase 4 management API as-is.
- **Management API the UI consumes** (real routes — do NOT invent): public — `GET/POST /api/auth/setup`,
  `POST /api/auth/login`, `GET /api/auth/oidc/login` (501), `GET /api/auth/oidc/callback` (501),
  `GET /healthz`, `GET /api/openapi.yaml` (raw spec, top-level, no auth). Authenticated —
  `GET /api/auth/me`, `POST /api/auth/logout`, `GET /api/definitions`, `GET/POST /api/apikeys`,
  `DELETE /api/apikeys/{id}`, `GET/POST /api/indexers`, `GET/PATCH/DELETE /api/indexers/{slug}`,
  `POST /api/indexers/{slug}/enable`, `.../disable`, `.../test`.
- **Contract quirks the UI MUST honour**: (1) indexer update is **PATCH** with merge semantics, not PUT.
  (2) `GET /api/indexers/{slug}` masks secret settings as the sentinel `"<redacted>"` with
  `secret:true`; to leave a secret unchanged, **PATCH back the same sentinel** (or omit the key); to
  rotate, send new plaintext. (3) `POST /api/indexers/{slug}/test` returns **HTTP 200 even on
  credential failure** with `{"ok":false,"error":...}` — branch on the `ok` field, only an unknown slug
  is 404. (4) `POST /api/apikeys` returns the plaintext `key` **exactly once** at mint — surface it
  immediately; it is never retrievable again. (5) The feed URL the UI displays for \*arr uses the
  `apikey=` **query param** (with `passkey=` alias) on `/api/v2.0/...` — distinct from the `X-API-Key`
  **header** the management API uses. (6) Strict decode: send only documented fields, one JSON object,
  no extra keys, or you get a 400.
- **Auth**: HttpOnly + SameSite=Lax `harbrr_session` cookie (the browser sends it; JS cannot read it).
  **No CSRF token anywhere** — do not send one. The `{"error":...}` envelope maps 401 (login/api-key),
  409 (conflict/already-setup), 400 (invalid/weak password), 404 (not found), 500 (generic).
- **Swagger UI is absent today** — only the **raw** spec is served at `GET /api/openapi.yaml`. The UI
  render (static assets pointed at that URL) is the missing piece.
- **`api_keys.last_used_at`** column exists but is **never written** (Phase 4 kept validation a pure
  read). A debounced touch-writer is the small backend half that feeds key-activity display.

## HARD RULES (do not work around)

- **STRICT FRONTEND GATE = COMMIT 1, BLOCKING, ZERO WARNINGS.** The moment TypeScript enters the repo,
  strict type-checking + strict linting + a format check are wired into **BOTH** the pre-commit hooks
  **AND** CI as a blocking gate — the same "strict CI / no god functions" discipline harbrr enforces for
  Go. `tsc --noEmit` must pass, `eslint … --max-warnings 0` (zero warnings, not just zero errors), and
  the format check must pass. This foundation lands **before any feature UI code**. No `// @ts-ignore`,
  no `eslint-disable`, no `any` (qui sets `@typescript-eslint/no-explicit-any: error`) without explicit
  approval recorded as a divergence.
- **Two HTTP contracts stay separate** (invariant #3): the SPA talks to the **management API only**. It
  must NOT import, embed, or call into the Torznab tree. `internal/web/torznab` ↔ `internal/web/swagger`
  / the SPA handler stay decoupled. OpenAPI changes → `make test-openapi`.
- **NEVER render a decrypted tracker credential in the UI.** Reuse the `<redacted>` sentinel end-to-end:
  GET masks, the edit form shows the sentinel, PATCH preserves-unless-changed. No secret may reach the
  DOM, the network tab, the console, a sourcemap, or a committed fixture. The minted API key plaintext
  is shown once and never persisted client-side.
- **The frontend build is reproducible and embedded.** `make build` / CI build the SPA into the dir Go
  embeds; the binary builds on a clean checkout without Node (committed `web/dist` + recreated
  `.gitkeep`). Lint/typecheck/format checks are **read-only** (`--noEmit`, `--max-warnings 0`,
  `--check`) — they never mutate in the gate, matching how `make lint`/`test` behave.
- **SQLite only** (invariant #5); pure-Go; no Postgres. Carry **every** Phase 4–7 hard rule forward.
- NO AI attribution/co-author/"Generated with" lines. Conventional commits; gofumpt-clean (Go side);
  interfaces ≤5 methods; no `map[string]any` for structured data; split god-functions
  (funlen/gocyclo/gocognit/nestif). Before EVERY commit: `make precommit` + `make build` green; tests
  always `-race -count=1`.
- Branch off main: `phase8/web-ui`. NEVER touch main (protected; required checks: `test`, `build`, the
  five `cross-build (...)`, `secret scan`; lint + CodeQL also run — and the new `web` job once added).
  One `docs/plan.md` item per commit; tick its box in the SAME commit, only when its tests are green.

## STRICT-CI FOUNDATION (Commit 1) — assert as a gate, lands BEFORE feature UI

This is the operator's non-negotiable centrepiece. Commit 1 scaffolds `web/` (configs + an empty SPA
shell that builds) AND wires the blocking gate into pre-commit + CI + the Makefile. No feature views in
this commit. Concretely:

- **Toolchain (qui-aligned, pinned):** React 19 + TypeScript ~6.0 + Vite 8 + Tailwind v4
  (`@tailwindcss/vite`, CSS-first) + shadcn/ui + lucide-react + TanStack
  (`react-query`/`react-router`/`react-table`/`react-form`) + `zod` + Vitest + RTL. Package manager
  **pnpm** (pin `packageManager: pnpm@…` in `web/package.json`); **Node ≥24** (`engines` + `web/.nvmrc`
  pinned to a concrete patch). Install with `pnpm install --frozen-lockfile`.
- **`tsconfig` strictness (solution-style, `web/tsconfig.{json,app,node,test}.json`):** `strict: true`,
  `noUnusedLocals: true`, `noUnusedParameters: true`, `erasableSyntaxOnly: true`,
  `noFallthroughCasesInSwitch: true`, `noUncheckedSideEffectImports: true`, `verbatimModuleSyntax:
  true`, `moduleResolution: "bundler"`, `noEmit: true`, `jsx: "react-jsx"`, `@/*` → `./src/*`. (Match
  qui exactly; if you opt into `noUncheckedIndexedAccess`/`noImplicitOverride`/`exactOptionalPropertyTypes`,
  flag it as a labelled decision.)
- **Linter/formatter (ESLint flat config, `web/eslint.config.js`):** `typescript-eslint` +
  `@eslint/js` + `@stylistic` + `eslint-plugin-react-hooks` + `eslint-plugin-react-refresh`. Copy qui's
  rules verbatim, including `@typescript-eslint/no-explicit-any: "error"`, double quotes, 2-space
  `indent` (`SwitchCase:1`), multiline `comma-dangle`, `object-curly-spacing: ["error","always"]`,
  `linebreak-style: ["error","unix"]`. Lint runs with **`--max-warnings 0`**. The `@stylistic` rules
  make this same run the read-only **format** check too — no separate Prettier/format tool (`eslint
  --fix` is the local fixer, never the gate).
- **Makefile targets (self-documenting `## name: desc`, tab-indented; `cd web && …` is fine in Make
  recipes):**
  - `## web-deps: install frontend dependencies (pnpm, frozen lockfile)` → `cd web && pnpm install
    --frozen-lockfile`
  - `## web-check: frontend gate — typecheck + lint+format (zero warnings)` →
    `cd web && pnpm exec tsc --noEmit` then `cd web && pnpm exec eslint . --max-warnings 0` (the
    `@stylistic` rules make this the format check too — no separate tool).
  - `## web-build: build the SPA into web/dist (embedded by the Go binary)` → `cd web && pnpm build`
  - Append `web-check` to the `precommit:` prerequisite line **exactly as `check-smoke-tag` was
    appended** (`precommit: fmt lint test check-smoke-tag web-check`), and wire `web-build` into the
    `build` ordering (`build: frontend backend`).
- **Pre-commit hook (`.pre-commit-config.yaml`, `web/`-scoped):** add one `repo: local` hook mirroring
  the Go local hooks' shape (`language: system`, `pass_filenames: false`) but scoped to the web tree:

      - id: web-check
        name: web-check (tsc + eslint + format-check)
        entry: make web-check
        language: system
        files: ^web/
        types_or: [ts, tsx, javascript, jsx, css]
        pass_filenames: false

  `files: ^web/` is the regex-scope analog of the Go hooks' `types: [go]` (fires only when web files are
  staged); `pass_filenames: false` matches every local hook; routing through `make web-check` keeps
  local == CI.
- **CI job (`.github/workflows/ci.yml`, new `web` job modeled on the `docker` "build then prove"
  pattern):** `runs-on: ubuntu-latest`, `timeout-minutes: 10`, `checkout@v6` →
  `pnpm/action-setup@v…` (version pinned via `web/package.json` `packageManager`) →
  `actions/setup-node@v…` with `node-version-file: web/.nvmrc`, `cache: pnpm`,
  `cache-dependency-path: web/pnpm-lock.yaml` → `pnpm --dir web install --frozen-lockfile` →
  `pnpm --dir web exec tsc --noEmit` → `pnpm --dir web exec eslint . --max-warnings 0` →
  `pnpm --dir web build` → **embed-drift** `git diff --exit-code -- web/dist`. Pin versions via the
  source-of-truth files (`node-version-file`/`packageManager`) the way Go jobs pin via `go-version-file:
  go.mod`. Use `--max-warnings 0` (the strict-gate analog of the golangci lint job).
- **Drift model (LOCKED — Model A):** commit `web/dist` to git so the Go binary builds without Node, and
  enforce freshness with `git diff --exit-code -- web/dist` after `pnpm build` (the embed-drift analog
  of the docker "prove it runs" step and the swagger drift test). Keep an empty `web/dist/.gitkeep` the
  build recreates. (Model B — generate `dist` in Docker + a Go embed test like `openapi_test.go` — is
  the alternative; if you switch, say so and keep the docker job as the integration backstop.)
- **Hygiene:** add `web/node_modules` to `.gitignore` AND `.dockerignore` (keep the build context
  small); `.editorconfig` already gives 2-space/space for `[*]` so TS/CSS inherit it — don't fight it.

## ORACLE / FIXTURES (decided): OFFLINE + deterministic — strict typecheck/lint gate, component + integration tests, embed drift

- **Strict static gate** (the centrepiece; runs in pre-commit AND CI, zero warnings): `tsc --noEmit`
  passes; `eslint --max-warnings 0` passes; the format check passes. A failing typecheck or a single
  warning fails the build — same posture as the Go god-function linters.
- **Component/unit tests** (committed; Vitest + RTL, jsdom): cover the secret-redaction edit flow (the
  form shows the `<redacted>` sentinel for secret fields and a PATCH leaving it unchanged sends the
  sentinel back / omits the key; rotating sends plaintext), the Test-action branch on `ok` (200 +
  `ok:false` renders failure, not success), the mint-once API-key surface, and the first-run vs login
  decision from `GET /api/auth/setup`.
- **Integration test** (committed; offline): at least one test drives the SPA against the **real
  management API** with a mocked/in-memory transport (MSW or a fetch stub seeded from
  `internal/web/swagger/openapi.yaml`) — never live network. Asserts the indexer add → grid → edit →
  enable/disable → delete round-trip and that no secret value crosses the wire.
- **Swagger-UI render assertion** (committed): a test asserts the embedded Swagger-UI route renders and
  points at `/api/openapi.yaml` (static asset bundle present; HTML route wired).
- **Embed/drift gate**: `git diff --exit-code -- web/dist` after a fresh `pnpm build` (Model A) proves
  the committed/embedded bundle is not stale. The Go side keeps `make test-openapi` green (the spec is
  the contract source of truth the UI renders).
- **Accessibility**: at minimum a lint-level a11y check on the key views (labels, roles, focus); state
  the depth. CI stays fully **offline and deterministic** — no live \*arr, no browser-against-internet
  smoke (defer any such smoke with a `[Tracked: …]` disposition).

## WORK LIST — each unchecked Phase 10 (web-UI) box is one item, in dependency order

1. **Strict-CI foundation** (Commit 1; enabling infrastructure, **no plan.md box** — say so in the
   commit): the qui-aligned `web/` scaffold that builds empty, plus the blocking strict gate wired into
   pre-commit + CI + Makefile per "STRICT-CI FOUNDATION" above. Nothing else merges until this is green.
2. **Web UI — auth surface + app shell**: first-run setup (`GET/POST /api/auth/setup`), login
   (`POST /api/auth/login`), session confirm (`GET /api/auth/me`), logout; the dashboard shell/nav. (No
   CSRF token; rely on the SameSite=Lax cookie.) *(plan.md "Web UI" box — display half.)*
3. **Web UI — indexer grid + health/status**: `GET /api/indexers` list with enabled/health/status,
   `GET /api/definitions` for the add picker, enable/disable
   (`POST /api/indexers/{slug}/enable|disable`), delete (`DELETE /api/indexers/{slug}`). *(Web UI box.)*
4. **Web UI — add/edit forms with the secret-redaction sentinel**: `POST /api/indexers` (add),
   `GET /api/indexers/{slug}` (edit, secrets masked as `<redacted>`), `PATCH /api/indexers/{slug}`
   (merge; send the sentinel to keep a secret, plaintext to rotate). The Test action
   (`POST /api/indexers/{slug}/test`, branch on `ok`). API-key mgmt (`GET/POST /api/apikeys`,
   `DELETE /api/apikeys/{id}`, mint-once display) + the \*arr feed-URL builder (`apikey=` query param).
   *(Web UI box.)*
5. **Web UI — manual search**: drive a search through the management surface and render results
   (title/size/category/seeders/grab link), honouring redaction. *(Web UI box.)*
6. **Swagger UI render**: serve a static Swagger-UI bundle (embedded) at a UI route pointing at
   `/api/openapi.yaml`. Low-risk, self-contained — the raw spec is already embedded + served. *(Web UI
   box — Swagger sub-clause.)*
7. **Stats / search-history DISPLAY + `api_keys.last_used_at`**: render stats/activity. **Split the
   plan.md "Stats / search history" box**: the event-log **schema + writers + query API** is the
   backend data layer and is a **prerequisite/sibling PR** — if it is not yet merged, scope this item to
   (a) the **debounced `api_keys.last_used_at` touch-writer** (the small backend half that lets the UI
   show key activity) + (b) the UI activity/stats views over whatever read API exists, and record the
   data-layer gap as `[Tracked: Phase 10 — stats data layer]`. *(plan.md "Stats / search history" box —
   display half only.)*

**Explicitly OUT of scope — separate follow-on Phase 10 PRs (one line each, do NOT build here):**
- \*arr application sync (sync contract + lifecycle; its own sub-plan) — `[Tracked: Phase 10 — *arr app-sync]`
- Jackett/Prowlarr migration import (reads `prowlarr.db` `Indexers.Settings` JSON) — `[Tracked: Phase 10 — Prowlarr import]`
- Native harbrr → autobrr push — `[Tracked: Phase 10 — autobrr push]`
- Cross-seed search backend — `[Tracked: Phase 10 — cross-seed]`
- Stats/search-history **data layer** (event-log schema + writers + query API) and **notifications**
  (Discord/webhook) — `[Tracked: Phase 10 — stats data layer / notifications]`
- Postgres — **NOT a Phase 10 item; out of the alpha roadmap entirely.** Demand-gated — build only when
  a real multi-instance user needs it. Do NOT touch it here beyond keeping `dbinterface` dialect-portable
  (the `Rebind` seam). See `docs/plan.md` → "Beyond the alpha — not scheduled (demand-gated)".
- **OIDC full implementation** (net-new config struct + provider client + session integration — the
  `/api/auth/oidc/*` endpoints are 501 and there is **no config seam** today; correct the
  `secrets/testdata/README.md` "only a config seam exists" claim when that PR lands) —
  `[Tracked: Phase 10 — OIDC]`. Until then the UI **hides OIDC** (treats both endpoints as unavailable).
  *(If the approved plan elects to include OIDC here, promote it to a numbered work-list item and tick
  the OIDC box; default is deferred.)*

## SUCCESS CRITERIA — assert as a gate

- The strict frontend gate is **blocking in BOTH pre-commit and CI, zero warnings**: `tsc --noEmit`
  passes, `eslint --max-warnings 0` passes, the format check passes, and a single warning or type error
  fails the build — and it landed **before** any feature UI code.
- The dashboard, in a browser against the running daemon, lists / adds / edits / enables / disables /
  deletes indexers; runs a **manual search** and renders results; runs the indexer **Test** action
  (branching on `ok`); shows indexer **health/status**; mints/revokes API keys (plaintext shown once);
  and handles first-run setup + login + logout — all via the **real management API**, no invented routes.
- The **`<redacted>` sentinel** round-trips correctly: GET masks secrets, the edit form shows the
  sentinel, PATCH preserves-unless-changed; **no decrypted credential** ever reaches the browser, the
  DOM, the network tab, the console, a sourcemap, or a commit.
- **Swagger UI** renders the embedded spec at its UI route; the **two HTTP contracts stay separate**
  (the SPA never touches the Torznab tree).
- The SPA is **embedded** in the Go binary and `make build` produces it; the binary builds on a clean
  checkout without Node; `git diff --exit-code -- web/dist` is clean after a fresh build.
- No CSRF token is sent; SameSite=Lax cookie auth works; OIDC is hidden (501) unless the plan included
  it.
- `make precommit` + `make build` green (incl. the frontend gate); the new CI `web` job green; all 5
  cross-builds green; contracts still separate; SQLite-only; PR ≤150 files.

## PER-ITEM LOOP (after plan approval; one commit per item)

(a) brief per-item plan consistent with the approved master plan; (b) IMPLEMENT + table-driven /
colocated tests beside it (component + the offline integration test where the behaviour allows); (c)
VERIFY `make precommit` + `make build`, `-race`, and `make web-check` (zero warnings); (d) ADVERSARIAL
REVIEW — ≥3 independent skeptics try to REFUTE it (XSS / output-encoding; secret leakage into the DOM /
network tab / console / sourcemap; the `<redacted>` round-trip on PATCH; the Test-action `ok`-vs-status
branch; mint-once key handling; auth bypass / broken access on the management API; the no-CSRF posture;
two-contract separation; bundle/embed correctness + drift; `any`/`@ts-ignore`/`eslint-disable` creep).
Fix every confirmed issue; re-verify. (If skeptic agents die on a spend limit, fall back to rigorous
inline self-review and SAY SO.) (e) COMMIT: one focused conventional commit; tick the box in the same
commit (Commit 1 ticks no box — it is enabling infrastructure).

## AFTER ALL ITEMS

- f) END-TO-END PHASE REVIEW + completeness critic ("which view / secret path / contract boundary /
  drift / a11y claim is unverified?"); close gaps. Record any divergence (the strict-CI gate vs qui's
  exact config; any extra TS strictness; the deferred OIDC + stats-data-layer gaps; the drift model
  chosen) with an explicit disposition in a Phase 10 / `web` testdata or `web/README.md` note and add it
  to `docs/divergences.md`. Add the Phase 10 web-UI improvements to `docs/highlights.md` (honestly
  labelled `[shipped]`/`[partial]`/`[planned]` — and do NOT claim "Prowlarr replacement").
- g) KEEP THE PR ≤150 FILES (CodeRabbit auto-skips above 150; a UI PR is at high risk — `node_modules`
  must be gitignored, but the lockfile + many components + the committed `web/dist` bundle count, so
  plan lockfile/asset hygiene and split a self-contained chunk into a second PR + note merge order if
  needed). Don't open multiple PRs + force-push in rapid succession (CodeRabbit ~1h rate-limit; it
  auto-reviews on PR-open, so do NOT post `@coderabbitai review` redundantly).
- h) OPEN ONE PR: `phase8/web-ui → main`, with a summary + testing checklist + a coverage table
  (strict-CI gate, auth/setup/login, indexer grid + health, add/edit forms + redaction sentinel, Test
  action, manual search, API-key mgmt + feed-URL builder, Swagger UI, stats display +
  `last_used_at`, embed/drift). No AI attribution.
- i) CI GREEN: push, fix until all required checks pass (test, build, cross-build ×5, secret scan, and
  the new `web` job). CI is fully offline.
- j) CODE REVIEW: let CodeRabbit's auto-review complete; address EACH finding (validate → fix +
  revalidate, or reply inline why it's skipped/intentional). Re-run CI.
- k) PAUSE: once CI + review are green, STOP. Do NOT merge. Wait for my review.

## FINAL REPORT

Items shipped (commit ids); confirmation the strict frontend gate (tsc `--noEmit` + eslint
`--max-warnings 0` + format check) is blocking in **both** pre-commit and CI, zero-warnings, and landed
as Commit 1 before feature UI; the toolchain + pinned versions as built vs qui; the UI surfaces built
vs the Web UI box scope (grid, add/edit + redaction sentinel, manual search, Test action, health,
API-key mgmt + feed-URL builder, Swagger UI, stats display); frontend + integration + a11y test
coverage; how the SPA is embedded/served and that the two contracts stayed separate; the drift model +
bundle/file-count vs the 150 cap; explicit confirmation that no decrypted credential reaches the
browser, DOM, network, sourcemap, logs, or a commit; known divergences + dispositions (incl. every
deferred Phase 10 box — OIDC, stats data layer, app-sync, import, push, cross-seed, notifications,
Postgres); and open questions.
