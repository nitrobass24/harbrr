# Security model

harbrr stores tracker **passkeys, cookies, API/auth keys, and download tokens**, plus its own
**web-UI login and management-API keys** — among the most sensitive data a self-hosted app holds, and
the place a careless indexer manager leaks first. The model **follows qui** (the autobrr-family
sibling harbrr is patterned on for API/DB/security): three credential *classes*, handled three
different ways. Conflating them — especially storing a login password the same way as a tracker
passkey — is the classic mistake, so the split is structural, not incidental.

Code: `internal/secrets` (the three-class model), `internal/http` (log/trace redaction),
`internal/auth` (authentication), `internal/config` (key sources), `internal/database` (file
permissions, key rotation).

## Credential classes (the core rule)

| Class | Examples | Mechanism | Recoverable? |
|---|---|---|---|
| **Login password** | the web-UI/admin password | **argon2id** (m=64 MiB, t=3, p=2, 16-byte salt, 32-byte key), PHC-string encoded, constant-time verify | **No** — one-way hash |
| **Bearer tokens** | management API keys, the *arr-facing Torznab `apikey`, session tokens | random 32 bytes, stored as a **SHA-256 hash**, shown to the user **once** | **No** — one-way hash |
| **Tracker credentials** | passkeys, login user/pass, cookies, tracker API keys, download tokens | **AES-256-GCM** at rest (harbrr must *replay* them to log into the tracker) | **Yes** — decrypted at request time |

Rule of thumb: **anything harbrr must replay is encrypted; anything it only needs to verify is
hashed.** The web-UI password and API keys are never stored in recoverable form, so a database leak
(or even a key compromise) never yields them.

- Passwords: `secrets.HashPassword` / `VerifyPassword` (`internal/secrets/password.go`) — argon2id
  parameters exactly as above, PHC-encoded, `subtle.ConstantTimeCompare`.
- Bearer tokens: `secrets.GenerateAPIKey` (32 random bytes) / `HashToken` (SHA-256)
  (`internal/secrets/token.go`); verification is a lookup of the presented token's hash against the
  stored hash (`internal/auth`). Plain SHA-256 is correct here: the input is a 256-bit random token,
  not a low-entropy password, so a slow KDF buys nothing.
- Tracker credentials: the `Keyring` (`internal/secrets/keyring.go`, `aead.go`) — see below.

## At-rest encryption (tracker credentials)

- **AES-256-GCM** (`crypto/aes` + `crypto/cipher`), 32-byte key, a **fresh random nonce per record**
  from `crypto/rand`, stored prepended to the ciphertext (`nonce‖ciphertext‖tag`, base64) in
  `*_encrypted` columns — qui's construction.
- **AAD bound to the row identity** — `"<instanceID>\x00<setting>"` — so a ciphertext cannot be copied
  or replayed across rows or fields. *(qui passes no AAD; harbrr adds it — a near-free hardening.)*
- A **`key_id` is stored with every record**, derived as the first 64 bits of the key's SHA-256
  (hex). It reveals nothing usable about the key, is stable across runs (so the canary verifies), and
  differs for a changed key (so a swap is caught).

## Key management

- The 32-byte key comes from a configured source — `secrets.encryption_key` (inline/env) or
  `secrets.key_file` (path); the two are mutually exclusive (config rejects setting both). This is
  **kept separate from session handling** — it has its own dedicated source.
- **First run with no key configured AUTO-GENERATES a keyfile** (32 random bytes, `0600`, under a
  `0700` `.keys` dir in the data dir) and uses it, logging where it was written and that it must be
  backed up *separately* from the database. So **encryption is always on.** True plaintext is reachable
  only behind an explicit `secrets.allow_plaintext` opt-in that **fails closed** if unset (a loud warn
  when set) — never the silent default. An empty `data_dir` with no configured key is rejected rather
  than falling back to a cwd-relative keyfile whose `key_id` would drift.
- A wrong or changed key **fails loud**: a **canary record** (`internal/secrets/canary.go`) is
  decrypt-verified at startup and harbrr refuses to touch secrets rather than silently dropping or
  re-encrypting garbage. A flip between plaintext and encrypted mode also trips it (the `key_id`
  differs). Losing the key means tracker creds must be re-entered (acceptable — they are re-enterable),
  which is exactly why the login password is *hashed*, not encrypted: a lost key must never lock the
  admin out of their own UI.
- **Key rotation is shipped** (`harbrr rotate-key`, `cmd/harbrr/rotate_key.go` +
  `internal/database/rotation.go`): an offline command run with the daemon stopped that re-encrypts
  every stored secret (and the canary) from an old key to a new key. "Every stored secret" is the whole
  set: `indexer_settings` plus every fixed-AAD surface enumerated in `database.SecretSurfaces`
  (`app_connections` + `announce_connections` — each with two secret columns —, `notifications`,
  `proxies`, `solvers`). Each column is re-sealed under the exact AAD its owning service uses, so the
  new key decrypts cleanly everywhere. It dry-runs (decrypts every row under the old key) before any
  write and applies the whole rewrite in a single transaction, so a wrong old key — or a mid-rotation
  failure — fails loud with the store untouched (never half-rotated). **Adding a new secret-bearing
  table means adding it to `SecretSurfaces` (or `AllSecrets` for a column-driven AAD); the rotation
  test in `cmd/harbrr/rotate_key_test.go` seeds every surface as the standing guard against that drift.**

## Web-UI / management-API authentication

- A **first-run setup** flow creates the single admin (argon2id password, minimum length enforced) —
  `internal/auth/service.go`.
- **Server-side sessions** via SCS with a database-backed store (`internal/app/app.go` →
  `sessionManager`): cookie `HttpOnly`, `SameSite=Lax`, path-scoped to the base URL, 30-day lifetime.
  `Secure` is computed once at startup (never mutated per-request) from `server.secure_cookie` OR an
  `https` `server.external_url` — either one marks it Secure; see `docs/reverse-proxy.md`.
- An **`X-API-Key`** header for programmatic clients, and a query-param `apikey` on the *arr-facing
  Torznab feed URL. Auth precedence (`internal/web/api/middleware.go`): `X-API-Key` → SCS session →
  auth-disabled mode.
- An **auth-disabled + IP-allowlist** mode for users behind an authenticating reverse proxy
  (`auth.mode=disabled` requires a non-empty `auth.ip_allowlist`, else harbrr refuses to serve).
  `X-Forwarded-For` is honored only from configured `auth.trusted_proxies`, taking the rightmost
  non-proxy hop so a client cannot forge an allowlisted IP. The same `auth.trusted_proxies` gates
  `X-Forwarded-Proto` trust for the feed/`/dl` self-URLs' request-derived scheme fallback (used when
  `server.external_url` is unset) — an untrusted peer can no longer force an `https` self-URL.

**CSRF** (`internal/web/api/csrf.go`). Cookie-authenticated **mutating** requests
(`POST`/`PUT`/`PATCH`/`DELETE`) require a **session-bound token** echoed in an `X-CSRF-Token`
header; a missing/mismatched token is `403`. This is a synchronizer token, not an
Origin/Referer check, on purpose — it is **origin-agnostic** (transparent to a reverse proxy that
rewrites `Host`/`Origin`) and **login-mechanism-agnostic** (a password login or a future OIDC
callback both just create a session, and both issue the token via the same helper). The token is
minted on login, stored in the session, and handed to the client in a **non-HttpOnly `harbrr_csrf`
companion cookie** (the session cookie is HttpOnly, so it can't be read by JS) — also returned by
`GET /api/auth/me`. Requests authenticated by `X-API-Key` or the auth-disabled/trusted-proxy mode
carry no forgeable ambient credential and are **exempt**. `SameSite=Lax` remains as defense in depth.

## Redaction & diagnostics

- **Log & trace redaction** (`internal/http/redact.go`). A **single shared vocabulary** of
  credential-shaped names (`passkey`, `api[_-]?key`, `rss[_-]?key`, `torrent_pass`, `cf_clearance`,
  `cookie`, `token`, `2fa`, …) drives every **name-matched** redaction surface at once — URL query
  params (`RedactURL`, `RedactURLIdentity`, `HostAndRedactedQuery`) and the `key=value` /
  `"key":value` scrubs in error strings (`RedactError`) — so the lists cannot drift apart. A URL's
  userinfo password (`scheme://user:pass@host`) is redacted **structurally** (always, independent of
  the name). The placeholder carries no length/prefix hint — `REDACTED` on the URL surfaces,
  `<redacted>` in the error-string scrubs. Served download/magnet links **do** legitimately carry
  passkeys (intended output) — those are never *logged*.
  **Standing obligation:** harbrr today never logs or traces raw request/response HEADERS or solver
  JSON BODIES, so no header- or JSON-body-redaction helper is currently wired (autobrr/harbrr#189
  deleted the unused `RedactHeader`/`RedactJSONBody`/`RedactProxyURL`, which had zero production
  callers). Any future change that logs or traces headers or a solver JSON body **must** reintroduce
  a redaction helper — with redaction tests — in the *same* PR, never after the fact.
- **Value scrub** (`internal/http/scrub.go`, `apphttp.ScrubValues`), the *other* half of the
  redaction seam alongside `RedactError`'s name-matched scrub above: instead of matching a
  field NAME, it replaces a caller-supplied credential VALUE wherever it appears in free text
  — a server response that echoes a submitted password/apikey/passkey back into an error or
  status message, which a name-matched scrub cannot catch. The VALUES to scrub are derived
  from the loader's authoritative `SettingsField.IsSecret` classifier
  (`loader.SecretValues(settings, config)`), so both the Cardigann engine's `login`/`search`
  stages and every native driver (`native.Base.Scrub`/`Base.ScrubErr`) share one derivation and
  one placeholder (`[redacted]`) — replacing ~13 hand-rolled per-driver `ReplaceAll` scrubs that
  had drifted (divergent placeholders, inconsistent substring-safety ordering).
  `Base.ScrubErr` preserves `errors.Is`/`errors.As` to the original error's sentinel
  (`login.ErrLoginFailed`, `*search.RateLimitedError`) through the scrub, so a redacted message
  never silently breaks the registry's health-event classification.
- **File permissions** (`internal/database/db.go`). Data dir `0700`; the database **and every SQLite
  side file** (`-wal`, `-shm`, `-journal`) `0600`, enforced regardless of umask.
- **Level-gated diagnostics.** `log.level` (`trace`|`debug`|`info`|`warn`|`error`) controls how much a
  failure reveals — always secret-safe. The level is **changeable at runtime** via the management API
  (`GET`/`PUT /api/config/log-level`, `internal/web/api/loglevel.go`); the accepted enum is shared with
  the config validator so the two can never diverge. At `trace`, a failed request logs only
  `op scheme://host: <redacted-cause>` (`internal/http/transporterror.go` → `SchemeHost` drops the
  path and query, because a native driver can carry a secret in a URL *path* segment). No raw response
  bytes and no request path/query are emitted at any level.
- **Config redaction.** `config.Config.String()` masks `secrets.encryption_key`/`key_file`, and the
  `secrets.Redacted` (`<redacted>`) sentinel is returned in place of a stored secret in management-API
  responses; re-submitting the sentinel on update means "keep the stored value" (mirrors qui).

## Not yet built (planned)

These were in the original design but are **not implemented**; don't document them as shipped:

- **OIDC** login ([#9](https://github.com/autobrr/harbrr/issues/9)).
- **Safe config/DB export/import** ([#91](https://github.com/autobrr/harbrr/issues/91)) with a
  `<redacted>`-by-default dump and a separately **passphrase-encrypted** include-secrets opt-in.
  (Backup/restore is tracked in autobrr/harbrr#91.) Only in-config redaction and the
  response sentinel exist today.
