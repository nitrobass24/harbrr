# Linting & type-checking — improvement proposal

Status: **proposal, not yet applied.** This captures concrete additions to the lint /
type-checking setup, prioritized for what this codebase actually is (an HTTP-heavy,
SQLite-backed, secrets-holding daemon that parses untrusted tracker responses). The current
policy is in [`linting.md`](linting.md); `.golangci.yml` stays the source of truth.

## Current state (baseline)

golangci-lint **v2** on **Go 1.26**, already strong: good type-safety (`forcetypeassert`,
`nilnil`, `exhaustive`, `wrapcheck`), the no-god-function complexity gates (`funlen`, `gocyclo`,
`gocognit`, `nestif`), and a broad family set (`gocritic`, `revive`, `gosec`, `errorlint`,
`perfsprint`, `prealloc`, `noctx`, `rowserrcheck`, `spancheck`, `loggercheck`, …).

Signals that motivate the additions below:

- **52 non-test files make HTTP calls**, and no linter checks response bodies are closed.
- **6 files** use `map[string]interface{}` / `map[string]any` — small enough to lock the rule in.
- **CI runs golangci-lint but not `govulncheck`.**

## Biggest gap: HTTP / DB resource leaks

For a daemon that hammers trackers, a leaked `resp.Body` is a real fd/connection leak, and
nothing currently catches it.

- **`bodyclose`** — every `resp.Body` is closed. Highest-value single add.
- **`sqlclosecheck`** — `sql.Rows`/`Stmt` are closed (complements the existing `rowserrcheck`).

## Tier 1 — real bug-catchers, low noise

- **`bodyclose`**, **`sqlclosecheck`** (above)
- **`errchkjson`** — flags types `json.Marshal` can't encode → silent `""` in the JSON API /
  Torznab responses
- **`nilnesserr`** (or `nilerr`) — the `if err != nil { return nil }` class of bug
- **`gocheckcompilerdirectives`** — validates `//go:build` / `//go:embed`; a typo silently
  disables them, and harbrr relies on both (vendored defs, build-tagged smoke)
- **`bidichk`** — trojan-source / bidi-unicode; harbrr processes untrusted tracker HTML and
  non-Latin definitions

## Tier 2 — enforce rules we already document but don't enforce

- **`interfacebloat`** — AGENTS.md says "interfaces ≤5 methods"; this makes it a gate.
- **`forbidigo`** — AGENTS.md says "avoid `map[string]interface{}` / bare `any`." Only 6 files
  use it today, so banning it (with a scoped allowlist) is cheap now and locks the rule in.
- **`usestdlibvars`** — `http.MethodGet`, status-code constants.
- **`durationcheck`** — duration-math bugs; harbrr is duration-heavy (TTLs, backoff, rate limits).
- **`makezero`** — append to a slice made with a non-zero length.
- **`contextcheck`** — context propagation through the daemon.

## Beyond golangci-lint

- **`govulncheck` in CI** — not currently run. Best single "be better" add for a going-public,
  secrets-holding service; low noise (only flags CVEs on call paths actually reached).
- **`nilaway` (Uber)** — static nil-panic analysis, deeper than any linter here, but a separate
  tool and noisier. Worth a trial, not a default.
- Keep the complexity gates as-is — they are doing their job.

## Rollout plan

1. Trial-run the **Tier 1** set (pure additions, no config risk) and triage the hits —
   `bodyclose` will tell us immediately whether there are latent leaks.
2. Land the clean Tier-1 linters + **`govulncheck`** in CI.
3. Add **Tier 2** with any needed scoped allowlists (`forbidigo`, `interfacebloat`).
4. Update [`linting.md`](linting.md) with whatever lands, and note test-file relaxations for the
   new linters as needed.
