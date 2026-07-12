# Secrets & daemon-foundation divergences

Where harbrr's daemon foundation — the secrets store, persistence, auth, and
server — deliberately or knowingly differs from its reference sibling
**autobrr/qui** or from the `docs/security.md` security model. Each entry carries
exactly one disposition (see `docs/divergences.md` for the vocabulary):
`[Deliberate]` (intentional design choice), `[Accepted]` (a kept difference, no
work planned), or `[Tracked]` (a real gap with a GitHub issue).

The behaviours below are pinned by tests in `internal/secrets`, `internal/auth`,
`internal/database`, `internal/web/api`, and `internal/server`.

## Improvements over qui (all `[Deliberate]`)

- **AEAD is AAD-bound.** Tracker-credential ciphertext is AES-256-GCM with AAD =
  `"<instanceID>\x00<setting>"`, so a blob cannot be replayed across rows or
  fields. qui passes no AAD. (`internal/secrets/aead.go`,
  `TestKeyringAADBoundToRow`.)
- **A `key_id` is stored per record.** `hex(SHA-256(key))[:16]` is recorded with
  every encrypted value and in the startup canary, enabling key rotation later and
  the fail-loud changed-key check now. qui has no key id. (`keyring.go`,
  `canary.go`, `TestVerifyCanaryFailsOnChangedKey`.)
- **The encryption key is separate from the session secret.** harbrr's encryption
  key comes from `secrets.encryption_key`/`key_file`/an auto-generated keyfile,
  and the DB-backed SCS session store uses random server-side tokens with no
  signing secret — so §9's "keep the encryption key separate from the session
  secret" holds by construction. qui derives its AES key from the session secret.

## Deliberate divergences from §9 / qui

- **Tracker `username` is stored plaintext.** §9 lists "login user/pass" as
  encrypted tracker credentials; harbrr encrypts the password (and cookies, api
  keys, passkeys, 2fa tokens, pins) but stores the **username** plaintext as a
  low-sensitivity identifier, so the edit UX can show it. The secret-field
  classifier (`loader.SettingsField.IsSecret`) reflects this; the corpus audit
  (`internal/indexer/cardigann/loader/testdata/secret_audit.txt`) pins the full
  classification. `[Deliberate]`
- **Session-bound CSRF token.** The security model (`docs/security.md`) requires "CSRF on
  cookie-authenticated mutating endpoints." harbrr now enforces a **synchronizer token**
  (`internal/web/api/csrf.go`): cookie-auth `POST`/`PUT`/`PATCH`/`DELETE` requests must echo the
  session's token in `X-CSRF-Token` (minted at login, handed to the client via the non-HttpOnly
  `harbrr_csrf` cookie + `GET /api/auth/me`); `X-API-Key`/auth-disabled callers are exempt;
  `SameSite=Lax` remains as defense in depth. A synchronizer token (not an Origin/Referer check)
  was chosen so it stays correct behind a reverse proxy and under a future OIDC login. Verified by
  `TestCSRF` (+ `TestSetupLoginLogoutFlow` cookie flags). `[Resolved]`
- **Single connection, not qui's dual pool.** The SQLite store uses one
  `*sql.DB` with `SetMaxOpenConns(1)`, fully serializing access (no `SQLITE_BUSY`
  / nested-tx) — simplest correct choice for a single-user daemon. qui's
  writer/reader pool split is a performance optimization not needed yet.
  `[Accepted]`
- **Custom SCS store, not `scs/sqlite3store`.** harbrr's session store is
  driver-agnostic (`internal/database/sessionstore.go`); the official
  `alexedwards/scs/sqlite3store` imports the cgo `mattn` driver, which would break
  the pure-Go `CGO_ENABLED=0` cross-build. `[Deliberate]`
- **Session-cookie `Secure` is operator-configured, not auto-detected.** §9 says
  "Secure behind TLS"; harbrr sets it from `server.secure_cookie` (default false)
  rather than auto-setting it per request from `X-Forwarded-Proto` (as autobrr
  does), because mutating the shared SCS cookie config per request is racy. The
  documented deployment is TLS terminated at a reverse proxy with the flag set.
  (`cmd/harbrr/serve.go` sessionManager.) `[Deliberate]`
- **One bearer-token namespace for the feed apikey.** The *arr-facing Torznab
  `?apikey` is validated against the same hashed `api_keys` table as the management
  `X-API-Key` (any minted key authorizes the feed), rather than a single dedicated
  Torznab key as in Jackett/Prowlarr. This matches §9 treating both as the
  bearer-token class and lets a user mint one key for both surfaces.
  (`cmd/harbrr/serve.go` apiKeyValidator → `auth.ValidateAPIKey`.) `[Deliberate]`

## Deferred product features → backlog, not this ledger

These are **unbuilt product features**, not behavioral divergences from Jackett, so they
are tracked as GitHub issues, not here: **OIDC auth** (`/api/auth/oidc/*`
returns 501 today; config seam only), **`api_keys.last_used_at` write** (validation is a
pure read; the auth event log populates it later), and **safe config/DB export-import**
(the `<redacted>` sentinel exists for the edit flow; the export/import path is deferred).

The one item that *shipped* and is kept here as a resolved record:

- **Interactive Swagger UI.** The embedded spec is served at `/api/openapi.yaml`
  and the rendered Swagger UI at `/api/docs` (`internal/server/server.go` →
  `swagger.UI()`), independent of the still-deferred web UI. `[Resolved]`

## Secret hardening

- **Key rotation lands.** The per-record `key_id` enabled it: a
  new offline `harbrr rotate-key` subcommand (run with the daemon stopped) holds
  the old + new keys explicitly — the single-key `Keyring` crypto core is
  **untouched** (no dual-key Keyring). It dry-runs (decrypts every `indexer_settings`
  secret under the old key, fail-loud BEFORE any write), then re-encrypts every row
  (re-sealed under the SAME AAD `<instanceID>\x00<setting>`) + the `app_meta` canary
  + `secrets_key_id` in ONE transaction. Wrong old key, plaintext mode, and
  empty-secret rows are handled (the last stay empty, only rekeyed). No decrypted
  credential or key byte reaches a log/error/the output. `[Resolved]`
  (`cmd/harbrr/rotate_key.go`, `internal/database/rotation.go`, `TestRotateKeys_*`).
- **Redaction audit.** Extended beyond `RedactURL`/`RedactHeader`. The wired,
  load-bearing change is the shared `internal/http.RedactError` chokepoint:
  per-handler test-error sanitizing was consolidated into it, and it is now used by
  both the API test endpoint (`internal/web/api/indexer_handlers.go`) and the
  `indexer_health_events.detail` writer (`internal/indexer/registry/adapter.go`). Two
  further helpers were added as **defensive, not-yet-wired** chokepoints — no
  production caller today, because the paths they guard avoid leaks structurally:
  `RedactJSONBody` would scrub FlareSolverr `/v1` request/response bodies
  (cookies/postData/userAgent/cf_clearance/response/headers, at any depth — JSON
  `RedactURL` can't reach), but the solver logs no bodies and never interpolates them
  into errors; `RedactProxyURL` would scrub the WHOLE proxy userinfo (user AND pass),
  but `buildTransport` never logs or errors a `proxy_url`. `[Resolved]`
  (`internal/http/redact.go`, `redacterror_test.go`, `redactbody_test.go`).
- **Tracing / stats-event-log redaction is vacuous.** §9 names "logs, errors,
  traces, and the stats event log" as redaction targets, but harbrr has **no
  tracing and no stats/event-log subsystem** — those targets do not exist, so the
  audit cannot wire redaction into them. Building them is out of scope.
  `[Accepted]` (revisit when the stats/event-log feature lands — autobrr/harbrr#103).
