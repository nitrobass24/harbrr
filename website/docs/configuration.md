# Configuration

harbrr reads configuration from a YAML file, environment variables, and command-line flags.
**Every value is optional** — the defaults below are what harbrr uses when a key is omitted.

A complete, commented [`config.example.yaml`](https://github.com/nitrobass24/harbrr/blob/main/config.example.yaml)
ships in the repo; copy it to `./harbrr.yaml` or `./data/harbrr.yaml`, or pass `--config <path>`.

## Precedence and environment variables

Precedence is **command-line flag > environment variable > config file > default**.

Environment variables are `HARBRR_`-prefixed with dots replaced by underscores:

| Config key        | Environment variable          |
| ----------------- | ----------------------------- |
| `server.port`     | `HARBRR_SERVER_PORT`          |
| `auth.mode`       | `HARBRR_AUTH_MODE`            |
| `log.level`       | `HARBRR_LOG_LEVEL`            |

!!! note "Runtime-tunable cache knobs"
    The `cache.*` values below are the **boot defaults**. Every one of them can also be
    changed at runtime — without a restart — via `GET`/`PUT /api/cache/config`. See the
    [search-results cache](features/search-results-cache.md) page.

---

## `server`

```yaml
server:
  host: 127.0.0.1        # listen host; use 0.0.0.0 in a container
  port: 7474
  base_url: ""           # serve under a subpath, e.g. "/harbrr" (no trailing slash)
  secure_cookie: false   # set true when reached over HTTPS (TLS-terminating proxy)
```

Set `secure_cookie: true` whenever harbrr is reached over HTTPS (for example behind a
TLS-terminating reverse proxy) so the session cookie carries the `Secure` attribute.

## `log`

```yaml
log:
  level: info            # trace | debug | info | warn | error
  format: console        # console | json
```

## `data_dir` and `database`

```yaml
data_dir: ./data         # data directory (created 0700); holds the db + keyfile
database:
  path: ""               # SQLite path; defaults to <data_dir>/harbrr.db
```

harbrr is SQLite-only. The data directory is created `0700`; the database and its
`-wal`/`-journal` side files are `0600`.

## `secrets`

```yaml
secrets:
  encryption_key: ""     # inline 32-byte key (hex or base64); prefer key_file/env
  key_file: ""           # path to a 32-byte key file (raw or hex/base64 encoded)
  allow_plaintext: false # opt into UNENCRYPTED storage; otherwise harbrr fails closed
```

Encryption of tracker credentials is **always on**. With no key configured, harbrr
auto-generates a keyfile at `<data_dir>/.keys/harbrr.key` (`0600`) on first run.

!!! warning "Back up the keyfile"
    Back the keyfile up **separately** from the database — losing it means re-entering every
    tracker credential. To store secrets unencrypted you must explicitly set
    `allow_plaintext: true`; otherwise harbrr fails closed and emits a loud warning.

## `auth`

```yaml
auth:
  mode: required         # "required" (login) or "disabled" (trust a reverse proxy)
  ip_allowlist: []       # e.g. ["10.0.0.0/8", "192.168.1.5"]
  trusted_proxies: []    # peers whose X-Forwarded-For is trusted, e.g. ["172.16.0.0/12"]
```

- **`required`** (default) — operators log in; the management API needs a session or an API key.
- **`disabled`** — harbrr trusts an authenticating reverse proxy and serves a synthetic admin
  to allowlisted client IPs. This mode **requires a non-empty `ip_allowlist`**.

Set `trusted_proxies` to the proxy peers whose `X-Forwarded-For` harbrr should trust when
resolving the client IP.

## `cache`

```yaml
cache:
  enabled: true          # set false to disable caching entirely (zero behavior change)
  rss_ttl: 5m            # TTL for an empty/RSS poll
  keyword_ttl: 30m       # TTL for a real keyword/id search
  thin_ttl: 2m           # shorter TTL when a search returns few results
  thin_threshold: 5      # result count at/below which thin_ttl applies (only shortens)
  refresh_ahead_pct: 80  # serve cached + refresh once past this % of the TTL
  cleanup_interval: 1h   # how often expired entries are reaped
```

These are the boot defaults; all are runtime-tunable via `PUT /api/cache/config`. The
[search-results cache](features/search-results-cache.md) page explains each knob in depth.

---

## What stays in the config file (by design)

Deploy-time and security-sensitive settings are deliberately **not** runtime-tunable: the
data directory, database path, listen address, base URL, the encryption key (it must stay
out of the database it protects), and the auth mode / IP allowlist / trusted proxies. Change
those in the config file (or environment) and restart.
