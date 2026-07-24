# Features

Everything harbrr does today, and everything it's going to do.

harbrr is a single Go binary that speaks Torznab and Newznab. You point Sonarr, Radarr, and
friends at it; it searches your trackers and hands back results. It reads the same Cardigann
tracker definitions Jackett and Prowlarr use — **byte-for-byte, straight from the upstream
snapshot** — so behavioural parity on the same input is the project's standing correctness gate,
not an aspiration.

Status key:

- ✅ **Shipped** — built, tested, and in the binary today.
- 🚧 **Pending** — designed and tracked, not built yet. Each links to its issue.

---

## What harbrr does that Prowlarr and Jackett don't

These are the differences that motivated building harbrr rather than contributing to what
already exists. Every one of them is shipped today.

### A real search-results cache ✅

harbrr remembers recent answers, and a cache hit means **the request never reaches your tracker
at all**. This matters more than a client-side cache because harbrr *is* the search server — it
sits at the one point in the chain where a duplicate request can actually be stopped.

It's not a naive key-value store:

- **Tiered TTLs** — RSS polls, keyword searches, and thin result sets each expire on their own
  schedule, because they go stale at different rates.
- **Stale-while-revalidate** — a nearly-expired entry is served immediately while a refresh runs
  behind it, so nobody waits on a cache boundary.
- **Negative caching** — "no results" is an answer worth remembering too.
- **Honest accounting** — harbrr counts the tracker requests it *prevented*, so you can see what
  the cache is actually buying you rather than taking it on faith.

The everyday case this fixes: two Sonarr instances (one 1080p, one 4K) polling the same tracker
for the same thing, forever. One tracker request instead of two, every cycle.

→ **[Search-results cache](search-results-cache.md)**

### Per-indexer request budgets, with reactive learning ✅

Set a query and grab limit per indexer, per day or per hour, and harbrr enforces it. The part
nobody else does: when a tracker tells harbrr it has hit a quota, harbrr **learns that cap and
respects it from then on** — even with no configuration at all. A budget-exhausted indexer prefers
serving a slightly stale cached result over going dark, and it is never mistaken for a broken
indexer.

### One tracker config, both your \*arrs and cross-seed ✅

harbrr treats cross-seed as a first-class consumer rather than something to be worked around. It
generates the cross-seed configuration for you, and a per-indexer freeleech toggle plus announce
push round it out. Configure the tracker once.

→ **[Cross-seed & freeleech](cross-seed-freeleech.md)**

### qui and autobrr as sync targets ✅

harbrr pushes indexer configuration into **qui** alongside the usual Sonarr/Radarr/Lidarr/
Readarr/Whisparr targets, and pushes releases to autobrr. harbrr is built for the autobrr family,
so its own family's tools are supported rather than being someone else's feature request.

→ **[App Sync](../guides/app-sync.md)**

### Honest pagination ✅

When something walks the feed page by page, harbrr returns stable, non-duplicating pages and
truthful counts — including the awkward cases (an offset past the end, a partial final page).
Quietly repeating or dropping releases across page boundaries is a silent data-loss bug, and it's
tested against explicitly.

→ **[Pagination](pagination.md)**

### A single binary ✅

No .NET runtime, no separate frontend service. One Go binary, SQLite, and a data directory.

---

## The full picture

### Search & serving

| Feature | Status |
|---|:--|
| Torznab + Newznab endpoints per indexer | ✅ |
| Cardigann engine at parity with the upstream definition format | ✅ |
| **599 trackers** — 556 Cardigann definitions + 25 native Go drivers | ✅ |
| Native drivers for trackers Cardigann can't express (AvistaZ family, Gazelle, HDBits, PTP, BTN, MAM, FileList, IPTorrents, Nebulance, AnimeBytes, GazelleGames, BeyondHD, TorrentDay, NZBIndex, …) | ✅ |
| Usenet (Newznab) indexers alongside torrents | ✅ |
| Search-results cache with tiered TTLs, SWR, and negative caching | ✅ |
| Failing-tracker circuit breaker | ✅ |
| Per-host rate limiting, honouring definition `requestDelay` and `Retry-After` | ✅ |
| Per-indexer request + grab budgets with reactive learning | ✅ |
| Correct pagination and result counts | ✅ |
| ID-based search (IMDb/TMDb/TVDB) and category mapping | ✅ |
| 18 further native drivers | 🚧 |
| Per-indexer query timeout | 🚧 [#371](https://github.com/autobrr/harbrr/issues/371) |
| Partial results — one failing indexer never voids a whole search | 🚧 [#372](https://github.com/autobrr/harbrr/issues/372) |
| Automatic failover across a tracker's known base URLs | 🚧 [#375](https://github.com/autobrr/harbrr/issues/375) |
| Per-indexer required release flags (freeleech, halfleech, …) | 🚧 [#385](https://github.com/autobrr/harbrr/issues/385) |
| Per-release language and subtitle attributes from definitions | 🚧 [#379](https://github.com/autobrr/harbrr/issues/379) |
| Definition format extensions (multi-select settings, ID-only search, category mapping gaps) | 🚧 [#380](https://github.com/autobrr/harbrr/issues/380) |

### Getting past the tracker's front door

| Feature | Status |
|---|:--|
| Cookie, form, and API-key login flows | ✅ |
| FlareSolverr integration for Cloudflare-guarded trackers | ✅ |
| HTTP and SOCKS proxy support, per indexer | ✅ |
| Passkey and credential redaction across all logs and traces | ✅ |

### Apps & download clients

| Feature | Status |
|---|:--|
| App Sync to Sonarr, Radarr, Lidarr, Readarr, Whisparr | ✅ |
| App Sync to **qui** | ✅ |
| Cross-seed configuration generation | ✅ |
| Release push to autobrr | ✅ |
| **10 download-client drivers** — qBittorrent, Deluge, Transmission, rTorrent, Flood, Synology Download Station, NZBGet, SABnzbd, qui, and blackhole | ✅ |
| Interactive search in harbrr's own UI, with working downloads | ✅ |
| Send a search result straight to a download client | 🚧 [#7](https://github.com/autobrr/harbrr/issues/7) |
| Sync download clients to apps | 🚧 [#237](https://github.com/autobrr/harbrr/issues/237) |
| Per-indexer reject-executable-payloads setting, synced to apps | 🚧 [#381](https://github.com/autobrr/harbrr/issues/381) |
| Configurable synced-indexer name template | 🚧 [#384](https://github.com/autobrr/harbrr/issues/384) |
| Per-request search-filter bypass for cross-seed callers | 🚧 [#376](https://github.com/autobrr/harbrr/issues/376) |
| Credential sync to Upbrr | 🚧 [#101](https://github.com/autobrr/harbrr/issues/101) |

### Web UI

| Feature | Status |
|---|:--|
| Indexer, application, and download-client management | ✅ |
| Interactive search, with a responsive mobile layout | ✅ |
| Instant substring / regex filter over search results | 🚧 [#374](https://github.com/autobrr/harbrr/issues/374) |
| Search-cache dashboard — tracker requests saved, hit ratio, TTL tiers | 🚧 [#373](https://github.com/autobrr/harbrr/issues/373) |
| Request-budget usage meters | 🚧 [#377](https://github.com/autobrr/harbrr/issues/377) |
| Global setting to hide adult categories | 🚧 [#383](https://github.com/autobrr/harbrr/issues/383) |
| Adjustable total cache size | 🚧 [#67](https://github.com/autobrr/harbrr/issues/67) |

### Visibility

| Feature | Status |
|---|:--|
| Per-indexer stats — queries, grabs, average latency, last query/failure | ✅ |
| Failures broken out by cause — auth, rate limit, parse, anti-bot, transport | ✅ |
| Per-indexer cache stats, including tracker requests prevented | ✅ |
| Discord and webhook notifications | ✅ |
| Search history and an event log | 🚧 [#103](https://github.com/autobrr/harbrr/issues/103) |
| Indexer value scoring — uniqueness, grab success rate, per-category breakdown | 🚧 [#378](https://github.com/autobrr/harbrr/issues/378) |
| Newznab API-limit auto-discovery | 🚧 [#377](https://github.com/autobrr/harbrr/issues/377) |

### Operating it

| Feature | Status |
|---|:--|
| Single binary, SQLite, Docker image | ✅ |
| Full config via file **and** environment variables | ✅ |
| Session, API-key, and **OIDC** authentication | ✅ |
| Secrets encrypted at rest, with a loud warning if no key is configured | ✅ |
| CSRF protection on cookie-authenticated surfaces | ✅ |
| Backup and restore | ✅ |
| Reverse-proxy support | ✅ |
| Complete OpenAPI spec + Swagger UI at `/api/docs` | ✅ |
| Import an existing Prowlarr or Jackett setup | 🚧 [#42](https://github.com/autobrr/harbrr/issues/42) |
| Import an existing NZBHydra2 setup | 🚧 [#382](https://github.com/autobrr/harbrr/issues/382) |
| Header-based forward auth behind a trusted proxy | 🚧 [#386](https://github.com/autobrr/harbrr/issues/386) |

---

## Side by side

Accurate as of **July 2026**, against Prowlarr and Jackett as they ship today. We track their
capabilities and will correct this page when it changes.

| | harbrr | Prowlarr | Jackett |
|---|:--:|:--:|:--:|
| Cardigann definition compatibility | ✅ | ✅ | ✅ |
| Torznab / Newznab | ✅ | ✅ | ✅ |
| Sync indexers into apps | ✅ | ✅ | — |
| General search-results cache | ✅ | — [^1] | — |
| Tiered TTLs + stale-while-revalidate | ✅ | — | — |
| Cache accounting (tracker requests prevented) | ✅ | — | — |
| Per-indexer request budgets | ✅ | ✅ | — |
| Budget caps learned from the tracker automatically | ✅ | — | — |
| qui as a sync target | ✅ | — | — |
| autobrr as a target | ✅ | — | — |
| Cross-seed as a first-class consumer | ✅ | — [^2] | — |
| Per-indexer timeout | 🚧 | — [^3] | — |
| Automatic base-URL failover | 🚧 | — | — |
| Instant regex result filter | 🚧 | — | ✅ |
| Import from Prowlarr / Jackett / NZBHydra2 | 🚧 | — | — |
| Language / subtitle release attributes | 🚧 | — | — |
| Single binary, no runtime dependency | ✅ | — | — |

[^1]: Prowlarr caches one narrow case (repeated anime-batch requests, for a few minutes). A
general search-results cache has been requested repeatedly and declined as low-benefit.

[^2]: Prowlarr's cross-seed workaround is to configure the same tracker twice — once filtered,
once not — which duplicates credentials, health state, and rate-limit pressure for one account.

[^3]: Prowlarr has a fixed 100-second global timeout. Making it configurable per indexer is its
single most-upvoted open request.

**Where Prowlarr is still ahead:** it is a mature project with years of production use, a large
community, and integrations harbrr hasn't built yet. harbrr is younger and pre-1.0. What harbrr
offers is the same tracker corpus with better behaviour at the points that cost you tracker
goodwill — caching, budgets, and rate limiting — plus first-class support for the autobrr family.

---

## Next

- **[Getting started](../getting-started.md)** — run harbrr and point an \*arr at it.
- **[Tracker coverage](../coverage.md)** — all 599 trackers and how far each is validated.
- **[Configuration](../configuration.md)** — every key and environment variable.
