# AGENTS.md

Repo rules for AI agents (Claude Code, etc.) and contributors working on **harbrr** — a Go,
single-binary, Cardigann-compatible Torznab/Newznab search provider for the autobrr family. Read this
fully before editing. The full design is in `@docs/ideas.md`; the build checklist in `@docs/plan.md`.

## Prime directive

harbrr's entire value is **behavioral parity with Jackett's Cardigann engine on the same input**.
The build order retires that risk first: **test harness first, engine second, product third**
(`docs/plan.md`). Do not build product surface (UI, app-sync, migration) before the engine passes its
parity gate (the "Definition of done" in `docs/ideas.md`).

## Collaboration

- Stay inside the requested scope. Do not implement review-suggested or extra changes without
  explicit approval.
- Treat other agent / CodeRabbit / reviewer feedback as input to discuss, not automatic action.
- harbrr is single-user self-hosted software. Prefer readable, maintainable code over paranoid
  guards for impossible states.
- Work **one `docs/plan.md` item at a time**; check its box only when its tests are green.

## Non-negotiable rules (enforced by hooks / CI — do not work around)

- **NEVER hand-edit vendored tracker definitions** under `internal/indexer/definitions/vendor/`. They
  are consumed byte-for-byte from Jackett. ALL behavioral differences are absorbed in the engine,
  never in the def files. Fixes go upstream to Jackett or into `internal/indexer/definitions/dropin/`.
  (A CI job (`vendor-guard`) and a pre-commit hook — both `scripts/check-vendor-untouched.sh` —
  block edits here regardless of editor/tool; refreshing the snapshot via `make vendor-defs` is fine.)
- **NEVER log, print, or commit secrets** — passkeys, cookies, API keys, download tokens. Definitions
  routinely put passkeys in URLs; redact secret query params and `Authorization`/`Cookie` headers in
  all logs and traces. (gitleaks + `scripts/check-no-secrets.sh` run in pre-commit and in CI via
  `.github/workflows/security.yml`.)
- **NEVER add AI advertising/attribution/co-author lines** to commits or PRs.

## Repo map

- Entry: `cmd/harbrr`
- Engine — a compiler-style pipeline, one package per stage under `internal/indexer/cardigann/`:
  `loader → mapper → template → filter → selector → dateparse → regexadapter → login → search →
  normalizer`; the serializer is `internal/torznab`. Keep stages decoupled; each owns its fixtures.
  Parity gate: `internal/indexer/cardigann/parity`.
- Definitions: `internal/indexer/definitions/` — `vendor/` (embedded Jackett snapshot, read-only) +
  `dropin/` (user overrides, take precedence).
- Native indexers: `internal/indexer/native/` (Avistaz family etc. — **post-parity**).
- Other: `internal/search`, `internal/http` (auth/session, solver interface, redaction),
  `internal/download` (go-qbittorrent), `internal/secrets`, `internal/database` + `dbinterface`.
- Docs: `docs/ideas.md` (full plan), `docs/plan.md` (checklist), `docs/architecture.md`,
  `docs/linting.md`.

Before changing cross-module data flow, service boundaries, API routing, or the engine pipeline
shape, read `docs/architecture.md`.

## Required commands

- Build: `make build` (Go binary to `bin/harbrr`)
- Tests: `make test` — `go test -race -count=1 ./...`. **Always `-race -count=1`.**
- Lint: `make lint` · auto-fix: `make lint-fix`
- Format: `make fmt` (gofumpt + goimports)
- **Before final, for any code change:** run `make precommit` (fmt + lint + test) and `make build`.
- OpenAPI changes under `internal/web/swagger`: regenerate and run the drift test (`make test-openapi`).

## Go / backend conventions (aligned with autobrr/qui)

- Go **1.26**. Keep `gofumpt`-clean (a strict superset of gofmt).
- Exports PascalCase; locals camelCase. Group interfaces by domain under `internal/<area>`.
- Prefer **explicit error handling**; wrap with context. Keep **interfaces small (≤5 methods)**.
- **Avoid `map[string]interface{}` / bare `any` for structured data — use typed structs.**
- No backward-compatibility shims unless requested.
- Tests beside code as `*_test.go`; **table-driven**, reuse fixtures. Test file writes:
  `os.WriteFile(..., 0o600)`. Go 1.22+: do not add `tt := tt` in parallel subtests.

## Code shape — no god functions (enforced)

- `funlen`, `gocyclo`, `gocognit`, `nestif` run in CI. Keep functions small and single-purpose; the
  pipeline architecture is designed for this. If a function trips a limit, **split it** — do not raise
  the threshold or add a blanket `//nolint` without approval (see `docs/linting.md`).
- Prefer behavior-bearing branches only; collapse `switch` cases that equal `default`; let `default`
  carry the common path. No documentation-only branches.

## Engine-specific rules

- **Regex:** RE2 (`regexp`) by default for ReDoS safety; route to `regexp2` (.NET semantics) only when
  the def opts in, the def's `language:` is non-Latin, the pattern fails RE2 compile, or it uses
  .NET-only constructs (backreferences, lookarounds, atomic/conditional groups, `(?<name>)`). The
  differential suite runs both engines on the same fixtures and is the gate. Never silence a parity
  diff by editing a def.
- **Dates:** all .NET date handling goes through `dateparse` (timezones, relative dates, localized
  names). Add a fixture for every new format.
- **Selectors:** `cascadia`/`goquery`; keep the selector fixture suite green — it is a standing
  compatibility check vs Jackett's AngleSharp, not a one-time verification.
- **Validation target:** match Jackett's normalized output on saved fixtures (offline). Per-def-vs-live
  correctness is the corpus's job, not ours.

## Database

- **SQLite only for now**, behind `internal/database/dbinterface`. **Do not implement Postgres yet** —
  keep the interface clean so it can be added later. SQLite migrations only.

## Security

- Secrets encrypted at rest (key via env var or keyfile); if no key is configured, plaintext is
  allowed but must emit a **loud startup warning** — never silent. Data dir/db `0700`/`0600`.
  Management API requires auth; CSRF on cookie-auth surfaces. Redact secrets everywhere (see
  non-negotiables).
- **Synthetic test-fixture secrets** (values that exist only to prove redaction) live exclusively in
  `*_test.go`, `testdata/**`, and the vendored Jackett snapshot. These paths are excluded from secret
  scanning in exactly two places that **must stay in sync**: `scripts/check-no-secrets.sh` and
  `.gitleaks.toml`. Both run in pre-commit and in CI (`.github/workflows/security.yml`). This never
  relaxes the "never commit real secrets" rule above.

## Commits / PRs

- Conventional commits: `feat(scope):`, `fix(scope):`, `chore(scope):`, etc. Keep commits focused.
- PRs: clear summary, testing checklist, no AI attribution lines.

## Final report

State which required checks ran, which were skipped/deferred and why, and any unresolved failures. Do
not claim a task complete while a required check (`make precommit`, `make build`, touched-package
tests) is known failing unless the user accepts the risk.
