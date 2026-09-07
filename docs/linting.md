# Linting & formatting policy

Tool output and `.golangci.yml` are the source of truth. This note covers the *why* and how to handle
judgment calls.

## Formatting

`gofumpt` (a strict superset of gofmt) + `goimports` with local prefix `github.com/autobrr/harbrr`.
Run `make fmt`. Stricter than autobrr/qui's plain gofmt, but gofmt-compatible, so the family
relationship holds.

## What's enabled and why

The set is autobrr/qui's config (for family consistency) plus harbrr additions:

- **No god functions (harbrr additions):** `funlen` (≤80 lines / 50 statements), `gocyclo`
  (cyclomatic ≤15), `gocognit` (cognitive ≤20), `nestif` (nesting ≤5). The pipeline architecture is
  designed so functions stay small; these keep it honest.
- **Extra type safety (harbrr additions):** `forcetypeassert` (no unchecked `x.(T)`), `nilnil`,
  `wrapcheck`. Plus qui's `errorlint`/`errname`/`exhaustive`/`unconvert` and golangci's defaults
  (`errcheck`, `govet`, `staticcheck`, `unused`, `ineffassign`).
- **Resource-leak & correctness (harbrr additions):** `bodyclose` (every HTTP response body is
  closed — harbrr is HTTP-heavy), `sqlclosecheck` (`sql.Rows`/`Stmt` closed), `bidichk`
  (trojan-source / bidi-unicode in untrusted tracker data), `gocheckcompilerdirectives` (a typo in a
  `//go:build`/`//go:embed` directive silently disables it, and harbrr relies on both),
  `durationcheck` (`time.Duration` math bugs), `makezero` (no `append` to a non-zero-length slice),
  `interfacebloat` as a **god-interface guardrail at 10 methods** (the "interfaces ≤5" convention
  above stays a review norm, since a couple of core seams legitimately sit at 6), and the footgun
  guards `wastedassign`, `reassign`, `predeclared`.
- **Family inheritance:** `gocritic`, `revive`, `gosec`, `perfsprint`, `prealloc`, `noctx`,
  `containedctx`, `fatcontext`, `copyloopvar`, `misspell`, `whitespace`, and others.
- **Policy enforcement:** `nolintlint` — every `//nolint` must name its linter, carry a reason
  comment, and still be needed; a directive whose linter no longer fires there is itself a finding
  (`--fix` removes those). This mechanizes the nolint policy below.

Dependency CVEs are covered separately by **`govulncheck`** (a job in `.github/workflows/ci.yml`),
which only flags vulnerabilities on call paths harbrr actually reaches.

## Handling a complexity finding

Default: **split the function.** A `funlen`/`gocyclo`/`gocognit` hit is the linter pointing at a
function doing too much — the pipeline gives natural seams to extract along.

Do **not**:
- raise a threshold in `.golangci.yml` to make a finding go away, or
- add a blanket `//nolint` across a package or file,

without explicit approval. A narrowly-scoped, **commented** `//nolint:funlen // reason` on a single
genuinely-irreducible function (e.g. a large exhaustive table) is acceptable when justified — but it
should be rare, and the comment must say why splitting would hurt readability.

## Tests

Test files relax `funlen`/`gocognit`/`gocyclo`/`forcetypeassert`/`wrapcheck`/`bodyclose` (table-driven
tests and helper assertions are legitimately long, and a short-lived test HTTP response leaking is
harmless — `bodyclose` matters in production code), and `gosec` **G101** (hardcoded-credentials) is disabled in
`*_test.go` only — synthetic fixture secrets live there (see AGENTS.md "Security"; the same paths are
allowlisted for gitleaks/check-no-secrets), and G101 stays fully active on production code. Everything
else still applies.
