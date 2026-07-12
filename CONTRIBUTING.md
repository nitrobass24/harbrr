# Contributing to harbrr

Thanks for your interest! harbrr is a single-binary, Cardigann-compatible Torznab/Newznab
search provider for the autobrr family. This guide covers how to build, test, and submit
changes. The full rules for the codebase live in [`AGENTS.md`](AGENTS.md) — read it before a
non-trivial change.

## Before you start

- **Design & roadmap.** [`docs/architecture.md`](docs/architecture.md) is the load-bearing
  design summary; the roadmap and forward work live in GitHub issues.
- **Prime directive.** harbrr's value is **behavioral parity with Jackett's Cardigann engine
  on the same input**. Correctness is measured offline against Jackett, not against live
  trackers — see the parity suite in `internal/indexer/cardigann/parity`.
- **File an issue first** for anything beyond a small fix, so we can agree on the approach
  before you invest time. New tracker support that needs a native driver is tracked under the
  [`native-driver`](https://github.com/autobrr/harbrr/labels/native-driver) label.

## Development setup

- **Go 1.26** and `make`. No cgo — harbrr is pure Go (`CGO_ENABLED=0`), including the SQLite
  driver, so it cross-compiles cleanly.
- Common commands:

  | Command | What it does |
  |---|---|
  | `make build` | build the binary to `bin/harbrr` |
  | `make test` | `go test -race -count=1 ./...` — **always** `-race -count=1` |
  | `make lint` | run golangci-lint (auto-fix: `make lint-fix`) |
  | `make fmt` | gofumpt + goimports |
  | `make precommit` | fmt + lint + test — run this before you push |

  Match the golangci-lint version CI pins (see `.github/workflows/ci.yml` / `make tools`) —
  an older local build can miss newer checks.

## Frontend (web/)

The management UI is a Vite + React + TypeScript SPA embedded into the binary; its stack and
conventions mirror [qui](https://github.com/autobrr/qui). You need **Node ≥ 22.12** and
**pnpm 10** (the exact version is pinned via `packageManager` in `web/package.json`).

| Command | What it does |
|---|---|
| `make web-dev` | Vite dev server with `/api` proxied to a running `./bin/harbrr` on :7478 |
| `make web-ci` | **the frontend gate** — exactly what CI runs: frozen install, type-aware ESLint, vitest, route-tree drift check, production build (type-checks via `tsc -b`) |
| `make web-build` | build `web/dist` so the next `make build` embeds the UI |

Run `make web-ci` before pushing any `web/` change. House rules that CI enforces: strict
TypeScript with no `any`, type-aware lint (floating promises must be `void`ed or awaited),
qui's formatting style, a committed `src/routeTree.gen.ts` after `pnpm generate:routes`, and
all API calls through the `src/lib/api.ts` client. [`web/README.md`](web/README.md) has the
full list — including the secret-handling contracts (`<redacted>` round-trip; never log or
rebuild download URLs).

## Non-negotiable rules

These are enforced by CI and hooks — please don't work around them:

- **Never hand-edit vendored definitions** under `internal/indexer/definitions/vendor/`. They
  are consumed byte-for-byte from Jackett; all behavioral differences are absorbed in the
  engine. Fixes go upstream to Jackett or into `internal/indexer/definitions/dropin/`.
- **Never commit or log secrets** — passkeys, cookies, API keys, download tokens. Redact
  secret query params and `Authorization`/`Cookie` headers everywhere.
- **No AI advertising / attribution / co-author lines** in commits or PRs.

## Submitting a change

1. Branch off `main`; keep commits focused.
2. **Conventional commits:** `feat(scope): …`, `fix(scope): …`, `chore(scope): …`, `docs(scope): …`.
3. Run **`make precommit` and `make build`** — both must be clean. For `web/` changes,
   **`make web-ci`** too.
4. Open a PR with a clear summary and a short testing note. Every substantive change should
   land with tests (table-driven, beside the code as `*_test.go`, reusing fixtures).
5. If your change adds or moves an HTTP route, update the OpenAPI spec under
   `internal/web/swagger` and run the drift test (`make test-openapi`).

## Reporting a security vulnerability

**Do not open a public issue.** See [`SECURITY.md`](SECURITY.md) for private disclosure via a
GitHub security advisory.

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
