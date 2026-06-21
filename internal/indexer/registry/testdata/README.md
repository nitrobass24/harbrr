# Registry & operational-safety divergences

Where harbrr's operational hardening — per-request timeouts, retry
backoff, per-host rate limits, per-indexer proxies, indexer health/status, and the
FlareSolverr anti-bot solver — deliberately differs from Jackett/Prowlarr or makes
a harbrr-additive design choice. Each entry carries one disposition (see `docs/divergences.md`):
`[Deliberate]`, `[Accepted]`, `[Resolved]`, or `[Tracked]`.

These behaviours are pinned by tests in `internal/indexer/registry`
(`pacedclient_test.go`, `proxy_test.go`, `health_internal_test.go`,
`health_integration_test.go`), `internal/indexer/cardigann/search`
(`ratelimit_test.go`), and `internal/database` (`health_test.go`).

## Design choices (`[Deliberate]` unless noted)

- **`doerFactory` widened to a `ClientParams` struct.** The earlier seam was a
  nullary `func() (search.Doer, error)` that could not vary the HTTP client per
  instance. It widens to `func(ClientParams) (search.Doer, error)` with
  `{Instance, Cfg, Timeout, RateInterval}`, so the paced client and the proxy
  client both ride one seam. A struct (not positional args) keeps future fields
  from re-breaking the `WithDoerFactory` Option. `[Deliberate]` (`registry.go`,
  `client.go`).
- **Rate limiter keyed per target HOST, in-process, no eviction.** A package-level
  `sync.Map[host]*rate.Limiter` (`x/time/rate`, `rate.Every(interval)`, burst 1,
  keyed by `req.URL.Hostname()`) mirrors qui's `sharedLimiters`. Vocabulary, to be
  precise: "host" is the **target tracker domain**, and the limiter is a single
  in-memory global inside the **one** harbrr daemon — it paces by destination, NOT
  by OS host, and it does **not** coordinate across separate harbrr
  processes/containers. The anti-blacklist unit is the tracker's view of us: one
  source IP hitting one domain. "Per host, not per configured indexer" matters only
  when the **same target domain is configured more than once** in the daemon — the
  common pattern of adding one tracker twice (different categories or freeleech
  modes), or a second account — so those configs share one budget and harbrr can't
  burst the domain at 2× the intended rate. In the ordinary one-config-per-domain
  case the per-host limiter is effectively per-config. When two configs on a domain
  declare different intervals the strictest (slowest) interval wins (`SetLimit`). The
  key space is bounded by configured domains, so the map cannot grow unboundedly and
  there is no evict-vs-`Wait` race; eviction is deliberately omitted. `[Deliberate]`
  (`pacedclient.go`).
- **Rate value from the def's `requestDelay`, else a 1s default — not yet
  user-tunable.** Jackett's `requestDelay` (seconds) sets the per-domain interval
  when the def declares it; otherwise a conservative 1s default. There is no
  per-indexer rate override or global default *setting* today, so a user cannot tune
  pacing for a domain they know to be strict or lax. Exposing a per-indexer override
  + a global default (the `ClientParams.RateInterval` seam is already plumbed for it)
  is deferred to the product-settings surface. `[Deliberate]`; user-configurable rate
  `[Tracked]`.
- **Per-instance request timeout from a `timeout` setting, else a 60s default.** A
  per-indexer `timeout` setting (a Go duration, e.g. `30s`) bounds the whole request
  via `ClientParams.Timeout`; an unset/invalid value falls back to 60s. Jackett uses
  one global SiteLink timeout, so a per-indexer override is harbrr-additive.
  `[Deliberate]` (`client.go` `resolveTimeout`).
- **Backoff retries ONLY 429/503, bounded, honoring `Retry-After`.** `avast/retry-go`
  with `Attempts(3)`; `Retry-After` (delta-seconds or HTTP-date) is honored and
  clamped to `[0, 5m]`; other non-2xx are not retried. The per-request `ctx`
  deadline bounds the SUM of waits + backoff sleeps, and a cancelled `ctx` aborts
  both `Wait` and a backoff sleep with no reservation leak. `[Deliberate]`.
- **New `rate_limited` + `parse_error` typed errors + classification.** Jackett has
  no such sentinels and `checkErrors` maps only HTTP 401. At the `doRequest` 429/503
  boundary (and in the paced client on retry exhaustion) harbrr mints a typed
  `search.RateLimitedError{StatusCode, RetryAfter}` — it carries the status and the
  honored Retry-After, holds **no URL by construction** (so it can never leak a
  passkey; the caller wraps it with a redacted URL) and `Unwrap()`s to the
  `search.ErrRateLimited` sentinel. It mints `search.ErrParseError` at the `Execute`
  parse boundary. The registry classifier keys on the four **sentinels** only (via
  `errors.Is`): `ErrLoginFailed→auth_failure`, `ErrSolverRequired→anti_bot` (the
  login-layer `detectAntiBot` is the *detector* that wraps this sentinel, not a
  classifier input), `ErrRateLimited→rate_limited`, `ErrParseError→parse_error`.
  `[Deliberate]`.
- **Health is an append-only event log; status is derived.** `indexer_health_events`
  records only the four failure kinds; `GET /api/indexers/{slug}/status` derives
  `healthy`/`unhealthy` from a 1h recency window. A fleet-wide `/api/indexers/status`
  is out of scope. `[Deliberate]`; fleet status `[Tracked]`.

## Proxies

- **HTTP + SOCKS5 (+socks5h) shipped.** HTTP via `Transport.Proxy`; SOCKS5 via
  `x/net/proxy` → `Transport.DialContext` (net/http's env proxy ignores SOCKS, so
  the dialer is explicit; the env `Proxy` is cleared for SOCKS5). `proxy_url` is a
  reserved secret (encrypted at rest); a bad config **fails loud without
  interpolating `proxy_url`** — every `buildTransport` error is a fixed,
  credential-free string (no path logs the proxy URL; pinned by `proxy_test.go`).
  `internal/http.RedactProxyURL` (whole-userinfo scrub — username AND password) is a
  defensive chokepoint ready if a path ever surfaces a proxy URL, but it has **no
  production caller today**. `[Resolved]`.
- **SOCKS4/SOCKS4a not supported.** `x/net/proxy` ships no socks4 dialer, so
  `socks4`/`socks4a` fail loud (use `socks5` or `http`). Demand-gated — HTTP+SOCKS5
  cover the dominant real-world proxies, and there is no committed phase to add
  SOCKS4. `[Accepted]`.
- **Proxy live end-to-end not verified.** No proxy in the test env; the proxy doer
  construction is fully offline-tested. `[Tracked]` — live validation.

## FlareSolverr solver

The solver itself (a point-to client against an external FlareSolverr instance, like
Prowlarr) is parity, not a divergence; these are the harbrr-additive choices around it.

- **`flaresolverr_url` is a reserved secret; `flaresolverr_max_timeout` is clamped.**
  Like `proxy_url`, `flaresolverr_url` is a reserved secret (always encrypted at rest
  — it may carry embedded auth). The per-solve budget `flaresolverr_max_timeout`
  (seconds, per-instance) defaults to 60s and is clamped to `(0, 180s]`; the solver's
  HTTP-client timeout is `maxTimeout + 30s` so a within-budget headless-browser solve
  is never aborted by the client. `[Deliberate]` (`flaresolverr.go`, `manage.go`).
- **Solve replays a browser-realistic header set, forcing manual decompression.**
  After the solver clears the interstitial, harbrr re-issues the real request with the
  solver's UA (`cf_clearance` is UA-bound — discard-and-replay, like Prowlarr) AND a
  fabricated `Accept` / `Accept-Language` / `Accept-Encoding: gzip, deflate` set (a
  gzip-only `Accept-Encoding` is a known anti-bot 403 trigger; a def's own headers
  win). Sending an explicit `Accept-Encoding` suppresses net/http's transparent
  decompression, so harbrr decompresses manually; for `deflate` it sniffs the 2-byte
  zlib header and accepts BOTH RFC 1950 zlib-wrapped and raw RFC 1951 DEFLATE — a
  deliberate deviation from RFC 9110 (which defines `deflate` as zlib-wrapped only),
  because many servers send raw. Output is unchanged (same decoded body).
  `[Deliberate]` (`login/solver.go` `withSolverReplayHeaders`, `login/login.go`
  `decompressBody`/`looksZlibWrapped`).
