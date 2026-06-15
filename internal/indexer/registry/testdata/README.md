# Operational-safety divergences (Phase 6)

Where harbrr's Phase-6 operational hardening — per-request timeouts, retry
backoff, per-host rate limits, per-indexer proxies, and indexer health/status —
deliberately differs from Jackett/Prowlarr or makes a harbrr-additive design
choice. Each entry carries one disposition (see `docs/divergences.md`):
`[Deliberate]`, `[Accepted]`, `[Resolved: Phase N]`, or `[Tracked: Phase N]`.

These behaviours are pinned by tests in `internal/indexer/registry`
(`pacedclient_test.go`, `proxy_test.go`, `health_internal_test.go`,
`health_integration_test.go`), `internal/indexer/cardigann/search`
(`ratelimit_test.go`), and `internal/database` (`health_test.go`).

## Design choices (`[Deliberate]` unless noted)

- **`doerFactory` widened to a `ClientParams` struct.** The Phase-4 seam was a
  nullary `func() (search.Doer, error)` that could not vary the HTTP client per
  instance. Phase 6 widens it to `func(ClientParams) (search.Doer, error)` with
  `{Instance, Cfg, Timeout, RateInterval}`, so the paced client and the proxy
  client both ride one seam. A struct (not positional args) keeps future fields
  from re-breaking the `WithDoerFactory` Option. `[Deliberate]` (`registry.go`,
  `client.go`).
- **Rate limiter keyed per HOST, process-wide, no eviction.** A
  `sync.Map[host]*rate.Limiter` (`x/time/rate`, `rate.Every(interval)`, burst 1)
  mirrors qui's `sharedLimiters`. Per-host (not per-instance) so two instances on
  one tracker host share one budget — the anti-blacklist unit is the host/IP. The
  key space is bounded by configured hosts, so the map cannot grow unboundedly and
  there is no evict-vs-`Wait` race; eviction is deliberately omitted.
  `[Deliberate]` (`pacedclient.go`).
- **Rate value from the def's `requestDelay`, else a 1s default.** Jackett's
  `requestDelay` (seconds) sets the per-host interval when present; otherwise a
  conservative 1s default. `[Deliberate]`.
- **Backoff retries ONLY 429/503, bounded, honoring `Retry-After`.** `avast/retry-go`
  with `Attempts(3)`; `Retry-After` (delta-seconds or HTTP-date) is honored and
  clamped to `[0, 5m]`; other non-2xx are not retried. The per-request `ctx`
  deadline bounds the SUM of waits + backoff sleeps, and a cancelled `ctx` aborts
  both `Wait` and a backoff sleep with no reservation leak. `[Deliberate]`.
- **New `rate_limited` + `parse_error` typed errors + classification.** Jackett has
  no such sentinels and `checkErrors` maps only HTTP 401. harbrr mints
  `search.ErrRateLimited` at the `doRequest` 429/503 boundary (and in the paced
  client on retry exhaustion) and `search.ErrParseError` at the `Execute` parse
  boundary, and the registry classifies `ErrLoginFailed→auth_failure`,
  `ErrSolverRequired/detectAntiBot→anti_bot`, `ErrRateLimited→rate_limited`,
  `ErrParseError→parse_error`. `[Deliberate]`.
- **Health is an append-only event log; status is derived.** `indexer_health_events`
  records only the four failure kinds; `GET /api/indexers/{slug}/status` derives
  `healthy`/`unhealthy` from a 1h recency window. A fleet-wide `/api/indexers/status`
  is out of scope. `[Deliberate]`; fleet status `[Tracked: Phase 8]`.
- **Stale "Phase 4" solver labels corrected to "Phase 6"** in the login package
  (PR #1). `[Resolved: Phase 6]`.

## Proxies

- **HTTP + SOCKS5 (+socks5h) shipped.** HTTP via `Transport.Proxy`; SOCKS5 via
  `x/net/proxy` → `Transport.DialContext` (net/http's env proxy ignores SOCKS, so
  the dialer is explicit; the env `Proxy` is cleared for SOCKS5). `proxy_url` is a
  reserved secret (encrypted at rest); a bad config fails loud; proxy URLs are
  whole-userinfo-scrubbed in any log (`internal/http.RedactProxyURL`). `[Resolved:
  Phase 6]`.
- **SOCKS4/SOCKS4a not supported.** `x/net/proxy` ships no socks4 dialer, so
  `socks4`/`socks4a` fail loud (use `socks5` or `http`). Demand-gated — HTTP+SOCKS5
  cover the dominant real-world proxies. `[Tracked: Phase 6 — SOCKS4]`.
- **Proxy live end-to-end not verified.** No proxy in the test env; the proxy doer
  construction is fully offline-tested. `[Tracked: deferred — proxy live retest]`.
