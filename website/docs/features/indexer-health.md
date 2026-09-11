# Indexer health, failover & usage

Three separate signals tell you how an indexer is doing. They answer different questions, and
harbrr keeps them apart on purpose.

| Signal | Question it answers | Where it shows |
|---|---|---|
| **Health** | Does this indexer work right now? | Health column, details sheet, `GET /api/indexers/{slug}/status` |
| **Failover** | Which host is harbrr actually talking to? | A **failover** pill beside the name, the details sheet, `effectiveBaseUrl` on the detail API |
| **Usage** | Is anything actually querying it? | Usage column, the **Idle indexers** dashboard tile, `GET /api/indexers/stats` |

## Health

Health is tri-state:

- **healthy** — something succeeded and nothing has failed since.
- **failing** — the newest failure still stands. `failingSince` marks when the streak began.
- **unknown** — nothing has been observed yet (never tested, never searched).

Every failure is classified by kind — `auth_failure`, `rate_limited`, `parse_error`,
`anti_bot`, `transport` — and each kind has its own backoff curve, so a dead network is not
punished like a dead tracker. A persistently failing indexer leaves rotation until its backoff
elapses; `disabledTill` on the status endpoint says when, and the `status:healthy` aggregate
feeds skip it meanwhile. Health is about the tracker and your credentials only; it never
implies that any application is using the indexer.

The short negative-TTL window in the [search-results cache](search-results-cache.md) and
the [circuit breaker](circuit-breaker.md) are related but separate mechanisms.

## Base-URL failover

Most definitions list several base URLs for a tracker. When the configured host fails in a
**host-shaped** way — DNS, connect, TLS, a dead gateway — harbrr tries the definition's other
hosts, and promotes a candidate only after it passes a **real search**. An auth failure or a
rate limit never triggers failover: those are the tracker's answer, not the host's.

- The configured host is **never overwritten**. A promotion is recorded as a
  `base_url_promoted` health event (not a failure), and `GET /api/indexers/{slug}` reports
  `effectiveBaseUrl` and `failoverBaseUrl` while it is in effect.
- The indexer table shows a **failover** pill beside the name while a promotion is active,
  and the details sheet names the host in use.
- To keep an indexer on its configured host, turn on **Pin to configured host** under its
  advanced options (the `failover_disabled` reserved setting). The web form, the API and the
  [add-indexer guide](../guides/add-indexer.md) all use the same setting.

## Usage

harbrr records queries and grabs per indexer, and the table shows the query count and the age
of the last query beside health. Two states get a distinct look:

- **Never queried** — zero queries. Almost always means an application was never pointed at
  this indexer, not that anything is wrong with harbrr or the tracker.
- **Idle** — quiet for **more than 7 days** after having been queried before. The same class of
  problem: something stopped asking.

The dashboard's **Idle indexers** tile counts both and links to the table, so an indexer that
nothing is using finds you instead of waiting in a detail view. A working-but-unused indexer
stays **healthy**; usage never feeds into the health verdict.
