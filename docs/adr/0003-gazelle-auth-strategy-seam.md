# Gazelle family: auth-strategy seam, sites stay data

The Gazelle native family (`internal/indexer/native/gazelle`) began as one driver with
sites as pure data: Redacted and Orpheus differ only in `siteDef(id, name, url, delay)` —
identical API-key-header auth against `ajax.php`, identical parsing. The AlphaRatio driver
(#131) introduced a second auth regime — form login with a persisted/rotating session
cookie, its own settings schema, caps, and expired-session diagnostics — and expressed it
as definition-id-keyed branching inside the shared package (~70 `alphaRatio*` references,
21 in `auth.go`). Behavior keyed by id in shared code is the accretion pattern the engine
architecture exists to prevent, and it does not stay small: four more Gazelle sites are
planned (DICMusic #28, Libble #29, GreatPosterWall #30, BrokenStones #31), and all four
use AlphaRatio's username/password regime.

The gazelle package gains an explicit **auth strategy** seam, and per-site variation is
expressed only as data plus composed strategy — never `if id == "..."` in shared files:

```go
// authStrategy owns how one site authenticates ajax.php traffic and how its
// session lives. Implementations are stateless per-call; session state persists
// through the driver's setting store.
type authStrategy interface {
    // Prepare attaches credentials/session to an outgoing request.
    Prepare(ctx context.Context, d *driver, req *http.Request) error
    // Recover handles an auth-classified failure (re-login, session rotation);
    // returns whether the request should be retried once.
    Recover(ctx context.Context, d *driver, cause error) (retry bool, err error)
    // Scrub returns the strategy's secret values for Base.Scrub extras.
    Scrub(d *driver) []string
}
```

Two implementations:
- `apiKeyAuth` — Redacted, Orpheus: `Authorization` header from the `apikey` setting;
  `Recover` is a no-op (keys don't rotate server-side).
- `formLoginAuth` — AlphaRatio and the planned #28–#31 sites: username/password form
  login, session cookie persisted via the driver's setting store, one re-login retry on
  auth failure, custom UA.

A site is declared entirely in `sites.go`: `siteDef` (id, name, URL, delay, settings,
caps) + strategy selection + an optional `parseProfile` hook (release-shaping quirks such
as AlphaRatio's group/tag profile) — the same "sites are data" contract the family started
with, with behavior variation composed instead of branched.

## Consequences

- `auth.go`/`parse.go`/`search.go` contain no per-site conditionals; adding a Gazelle
  site is a `sites.go` entry + (at most) a new strategy or profile hook.
- #28–#31 each become: siteDef + `formLoginAuth` + caps table + optional profile — no
  shared-file edits.
- A third auth regime (if one ever appears) is a third strategy, not a wider if-ladder.
- The strategy interface stays ≤5 methods per the repo's interface conventions; if a site
  needs more than Prepare/Recover/Scrub + a parse profile, that's a signal it wants its
  own package, not a fatter interface.
