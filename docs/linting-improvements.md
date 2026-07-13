# Linting & type-checking — status & backlog

**Status:** the clean subsets of Tiers 1–3 shipped in `autobrr/harbrr#45`. This records what
landed, what was **declined** (with the reason), and the **open/deferred** items with enough
context to pick them up later. [`linting.md`](linting.md) is the living policy; `.golangci.yml`
is the source of truth.

Baseline it built on: golangci-lint **v2** on **Go 1.26** — good type-safety (`forcetypeassert`,
`nilnil`, `exhaustive`, `wrapcheck`), the no-god-function gates (`funlen`/`gocyclo`/`gocognit`/
`nestif`), and the family set (`gocritic`, `revive`, `gosec`, `errorlint`, `perfsprint`, …).

To pick up any open item: `golangci-lint run --default=none --enable=<linter> ./...`, triage the
hits, fix or allowlist them, then add the linter to `.golangci.yml` + `linting.md`. **Match the
CI-pinned golangci-lint version** (currently `v2.12.2`, in `.github/workflows/lint.yml` / `make
tools`) — an older local build silently misses newer checks (e.g. gosec G124).

## Shipped (#45)

- **Resource-leak / correctness:** `bodyclose` (1 production hit — a false positive where the
  paced client hands the body to its caller; scoped `//nolint`; relaxed in `*_test.go`),
  `sqlclosecheck`, `bidichk`, `gocheckcompilerdirectives` — all others zero findings.
- **Duration / slices / interfaces:** `durationcheck`, `makezero`, `interfacebloat` at **10**
  (god-interface guardrail; the "≤5" convention stays a review norm — torznabhttp.Indexer sits at 6).
- **Footgun guards:** `wastedassign`, `reassign`, `predeclared` (fixed one real hit — a smoke
  variable named `comparable` shadowing the builtin).
- **Dependency CVEs:** a **`govulncheck`** job in `security.yml`.

## Declined / deferred (open — with context)

Each line: the trial count, why it isn't in, and what enabling it would take.

- **`errchkjson`** *(4)* — mostly deliberate `ResponseWriter` encodes where the error is
  intentionally ignored (`web/api/encode.go`, `appsync/servarr.go`, `searchcache_key.go`).
  *Enable by:* handling or `//nolint`-ing those 4 sites. Low value unless we want to guard
  `json.Marshal` of dynamic types.
- **`nilerr` / `nilnesserr`** *(5)* — **false positives** on the intentional degrade-to-default
  parse pattern (`flexInt`/`flexBool` `if err != nil { return nil }` in the native parsers). Not a
  fit; only worth it if each degrade site is annotated.
- **`forbidigo`** (the "avoid bare `any` / `map[string]interface{}`" rule) — the ~6 files that use
  dynamic `any` (`selector/jsonpath.go`, `loader/schema.go`, `filter/json_filters.go`,
  `login/flaresolverr.go`, `http/redact.go`, `config/config.go`) parse **arbitrary JSON**, which is
  the legitimate exception AGENTS.md already carves out ("…for *structured* data"). *Enable by:*
  configuring forbid patterns + allowlisting those dynamic-JSON files. Left as a **review norm**.
- **`contextcheck`** *(7)* — one real false positive (`server.go` shutdown deliberately uses a
  fresh `context.WithTimeout`) plus test helpers (`mintKey`). *Enable by:* `//nolint` the shutdown
  site and thread context through the test helpers.
- **`interfacebloat` at ≤5** — `torznabhttp.Indexer` legitimately has 6 methods; `native.Driver`
  is split into the `Searcher` (5) + `Tester` roles. Shipped at 10; to tighten to the documented
  ≤5 you'd split the remaining `Indexer` seam (or accept it). Review norm for now.
- **`usestdlibvars`** *(6, all tests)* — magic HTTP status numbers (`429`/`200`/`503`) →
  `http.StatusXxx` in `ratelimit_test.go` / `pacedclient_test.go`. Cosmetic, test-only.

### Tier-3 opinionated volume (dedicated cleanup pass if ever wanted)

- **`paralleltest`** *(50)* — tests/subtests without `t.Parallel()`. Big mechanical change, and not
  all tests should be parallel (shared state) — needs per-test judgment.
- **`thelper`** *(22)* — test helpers missing `t.Helper()`. Purely mechanical.
- **`intrange`** *(17)* — Go 1.22 `for i := range n` modernization. Low-risk mechanical churn.
- **`unparam`** *(13)* — **mixed, and the most worth cherry-picking:** some are real dead returns
  (`appsync` `quiDriver.do`/`servarrDriver.do` return an unused `int`; `jsonpath.resolveRowsArray`
  returns an always-nil error) worth removing on their own; the rest is test-helper noise.
- **`tparallel`** *(5)* — small; parallel-subtest correctness.

## Beyond golangci-lint

- **`nilaway`** (Uber) — static nil-panic analysis, deeper than any linter here, but a separate
  tool and noisier. Not yet trialed; worth a future spike, not a default gate.
- Complexity gates stay as-is — they're doing their job.
