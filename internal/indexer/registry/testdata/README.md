# Registry & operational-safety divergences

Where harbrr's operational hardening — per-request timeouts, retry
backoff, per-host rate limits, per-indexer proxies, indexer health/status, and the
FlareSolverr anti-bot solver — deliberately differs from Jackett/Prowlarr or makes
a harbrr-additive design choice. Each entry carries one disposition (see `docs/divergences.md`):
`[Deliberate]`, `[Accepted]`, `[Resolved]`, or `[Tracked]`.

These behaviours are pinned by tests in `internal/indexer/registry`
(`pacedclient_test.go`, `proxy_test.go`, `health_internal_test.go`,
`health_integration_test.go`, `searchcache_breaker_test.go`,
`searchcache_stats_internal_test.go`), `internal/indexer/cardigann/search`
(`ratelimit_test.go`), and `internal/database` (`health_test.go`,
`searchcache_test.go`).

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
  pacing for a domain they know to be strict or lax. `[Deliberate]`. Exposing a
  per-indexer override + a global default (the `ClientParams.RateInterval` seam is
  already plumbed for it) is a product feature, not a divergence — see
  autobrr/harbrr#104 (user-configurable request rate).
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
  `healthy`/`unhealthy` from a 1h recency window. `[Deliberate]`. A fleet-wide
  `/api/indexers/status` roll-up is a product feature, not a divergence — see
  autobrr/harbrr#102 (fleet-wide indexer status).
- **Negative-result circuit breaker on the search cache.** After a live search to a
  configured instance fails, harbrr opens a per-instance breaker for a short window
  (`negative_ttl`, default 1m; a rate-limit response extends it to its `Retry-After`).
  While open, a cache **MISS** for that instance short-circuits to the recorded typed
  error instead of re-driving the tracker — so a down/rate-limited tracker is spared
  being re-hit by every consumer (anti-thundering-herd). Jackett/Prowlarr have no such
  breaker. Scope choices: (1) **only a MISS consults the breaker** — a still-fresh
  positive cache entry is always served, so an open breaker never blanks out cached
  results; (2) it trips on **any** live search error **except a caller-cancelled
  context** (`ctx.Err()`), because at the cache layer a non-nil `Search` error means the
  tracker returned nothing usable and re-driving it for every other consumer only pesters
  it — the short, self-healing window (the first request after it lapses probes live) and
  the `CacheBypass` operator override bound the cost; (3) keyed by **instanceID** (the
  cache layer's unit), not host; (4) the stored error is the **same typed,
  redaction-safe** error the live path returns and is **never logged**. `negative_ttl` is
  runtime-tunable (DB-backed app-settings; `0s` disables, taking effect immediately).
  `[Deliberate]` (`searchcache_breaker.go`, `searchcache.go`; pinned by
  `searchcache_breaker_test.go`).
- **Per-indexer cache observability.** `GET /api/cache/stats` exposes `trackerHitsSaved`
  (durable tracker requests served from cache), `breakerSuppressed`, and a `byIndexer[]`
  breakdown (hit ratio, hits saved, breaker open-state) — harbrr-additive metrics with no
  Jackett analogue. The surface reads only counts/timestamps and the indexer slug/name,
  never the cached payload. `[Accepted]` (`searchcache_manage.go`,
  `database/searchcache.go`; pinned by `searchcache_stats_internal_test.go`).

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

## Cross-seed freeleech (serve-time filter + announce source)

The freeleech-bypass feature renders the engine with `freeleech` cleared (so the cache
holds the FULL catalog) and applies "freeleech only" as a SERVE-TIME view over
`downloadVolumeFactor == 0`. The audit of all 348 vendored defs with a `freeleech`
setting found this parity-exact except for the two cases below.

- **`scenetime-api.yml` honor feed is empty under the full-fetch model.** It is the lone
  vendored def whose freeleech signal is expressed only by gating the
  `downloadvolumefactor` *search input* on `.Config.freeleech` (not a per-row marker), so
  a full fetch (freeleech cleared) returns every item with the default factor and the
  serve-time `dvf==0` filter keeps nothing. Every other freeleech def stamps
  `downloadvolumefactor` per-row from its own free marker, independent of the setting, so
  the filter reconstructs the exact freeleech subset. `[Deliberate]` — the caching win
  (one tracker fetch shared by the honor + bypass feeds + the announce source) is worth
  one obscure tracker's honor feed; use the bypass `/full` feed for SceneTime cross-seed.
  (`internal/indexer/registry/adapter.go`, `filterFreeleechOnly` + `indexerAdapter.Search`.)
- **Honor-feed pagination dilution above one page.** harbrr does a single engine fetch
  (default page = max = 100) and filters it to freeleech at serve time, so a search whose
  full result set exceeds one page shows fewer freeleech items on the honor feed than a
  Jackett freeleech-only fetch would (which would fill a whole page with freeleech). Most
  searches return well under 100, so it is invisible in practice; the bypass feed is
  unaffected. The only deep-paging driver (newznab/usenet) has no freeleech setting, so
  the related has-more-floor interaction is unreachable. `[Deliberate]`
  (`internal/indexer/registry/adapter.go`, the "Paging note" on `indexerAdapter.Search`; the
  single-engine-fetch model is the shared "Better pagination support" design, autobrr/harbrr#3).
