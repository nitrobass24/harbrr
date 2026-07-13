# Native drivers share a deep base with a required per-call status classifier

Every native driver used to re-implement the same scaffold: constructor wiring, an HTTP
helper with host-only redaction, a per-endpoint status switch, and a capped body read.
`native.Base` (`internal/indexer/native/base.go`) now owns all of it behind
`NewBase` + `Do`/`DoDownload`, and a driver keeps only its request generator and response
parser. The load-bearing choice inside that base: **status classification is a required
per-call parameter** (`Do(ctx, req, Classify)`), not per-driver configuration and not a
separate helper.

The reason is that the tracker corpus has real per-driver *and per-endpoint* variance in
what statuses mean — 401/403 is an auth failure on most trackers, but 403 is a spent rate
budget on HDBits/newznab (misclassifying it would record `auth_failure` health for working
credentials), 403 is an expired session on MyAnonamouse (which has no 401), and AvistaZ
uses 412 on search but 422 on its login endpoint. A required parameter makes forgetting
classification impossible and keeps the variance visible at the call site.

## Considered options

- **Configure the dialect once at `NewBase`** — cleaner call sites, but AvistaZ's
  per-endpoint variance forces an override mechanism anyway, yielding two paths instead of
  one.
- **Split helpers (`Do` for transport, `ClassifyStatus` separately)** — maximally
  composable, but a driver *can* forget the classify call, which is exactly the
  discipline failure the base exists to remove.

## Consequences

- Base errors are **caller-ready by design**: family-prefixed, host-only-redacted
  (`SchemeHost` + `RedactURLError`), and sentinel-bearing (`login.ErrLoginFailed`,
  `*search.RateLimitedError`, context sentinels survive `errors.Is`). Drivers return them
  unwrapped; `wrapcheck` ignores the native package for this reason — re-wrapping would
  double the family prefix.
- `Do` returns the response header shell **alongside** a classified-status error, so a
  driver that must see every response's headers (MyAnonamouse capturing a rotated
  `mam_id` off a 403/429) loses nothing to classification.
- `DoDownload` exists as a sibling rather than a flag because the grab path has different
  body semantics: a torrent past the cap is an error (`ErrDownloadTooLarge`), never a
  silent truncation, while API reads truncate at `MaxBodyBytes` like the pre-base drivers.
