# harbrr web UI

The single-page management dashboard, embedded into the harbrr binary at build time.
Stack mirrors [qui](https://github.com/autobrr/qui): Vite + React 19 + TypeScript
(strict) + Tailwind CSS v4 + shadcn/ui + TanStack Router/Query. The full scope and
architecture live in [`docs/webui-scope.md`](../docs/webui-scope.md).

## Developing

```sh
make build && ./bin/harbrr        # backend on :7478 (run from outside the repo, or pass --data-dir)
make web-dev                      # Vite dev server; /api and /healthz proxy to :7478
```

The embedded build is what ships: `make web-build` writes `dist/`, and the next
`make build` embeds it (`web/build.go`). A gitkeep-only `dist/` is valid for Go-only
work — the binary then serves a "frontend not built" page.

**Before pushing anything under `web/`:** `make web-ci` — the exact CI job: frozen
install → type-aware ESLint → vitest → route-tree drift check → production build
(which type-checks via `tsc -b`).

## Conventions that will fail your PR if skipped

- **Routes are file-based** (`src/routes/`, TanStack Router). After adding/renaming a
  route file, run `pnpm generate:routes` and commit `src/routeTree.gen.ts` — CI diffs it.
- **No `any`** (`@typescript-eslint/no-explicit-any` is an error) and the type-aware
  ESLint tier is on: floating promises must be `await`ed or explicitly `void`ed.
- **Style mirrors qui** (enforced by `@stylistic`): double quotes, 2-space indent,
  multiline trailing commas. `pnpm format` fixes most of it.
- **API calls go through `src/lib/api.ts`** — the single choke point for the base
  path, CSRF header, error envelope, and 401 handling. Never `fetch` directly.
- **Secrets:** fields the server redacts arrive as the literal `<redacted>` sentinel.
  On PATCH, an untouched sentinel is sent back verbatim (keep-stored contract —
  `settings-payload.ts` owns this for indexer settings); on POST it is stripped.
  Never log request/response payloads, and never construct or log feed//dl URLs —
  render server-returned download links verbatim (they may carry sealed credentials).
- **shadcn/ui components** live in `src/components/ui/` and follow upstream shadcn
  shape (mirrored from qui); domain components live in `src/components/<screen>/`.

## Tests

vitest + Testing Library (`pnpm test`). jsdom shims live in `src/test/setup.ts`.
Presentational components take data via props so tests feed fixtures directly —
see `IndexersTable.test.tsx` or `settings-payload.test.ts` for the house style.
