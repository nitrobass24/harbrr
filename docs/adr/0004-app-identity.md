# App identity: configure an app once, use it on every surface

## Context

harbrr connects to external apps from three independent surfaces: **app-sync**
(`app_connections` — push indexer config into *arr/qui), **announce** (`announce_connections`
— push new releases to cross-seed/qui), and **download clients** (`download_clients` — hand
grabbed releases to qBittorrent/qui/…). Each surface stored its own copy of the app's identity
— base URL + credential, the credential AES-GCM-sealed under that surface row's own id as AAD
(`connresource.Lifecycle`) — and its own lifecycle. When the *same* app was used from two
surfaces (a qui instance that is both an app-sync target and a download client), the user
configured it twice, and the credential was stored twice.

The bridge was **copy-seeding**: #72 seeded an announce target from a qui app-connection
(`appsync.QuiSeed` + `POST /api/app-connections/{id}/announce-target`); #266 proposed extending
that to all three surfaces, both directions. Copy-seeds deliver the UX ("never type twice") but
**duplicate the credential**: rotating a key means touching every surface that copied it, and a
stale copy silently keeps authenticating with the old key.

The family-wide product constraint (2026-07-18): **a user never sets up the same app twice.**
Any surface is one click from any other, in any direction. Copy-seeds satisfy the *entry* half
but not rotation.

## Decision

Introduce a first-class **App**: `(kind, base_url)` identity + one sealed credential + the app's
vantage onto harbrr, stored **once**. The three surface tables reference an App by `app_id` and
keep only their surface-specific fields.

### 1. The `apps` table & `internal/apps` service

```
apps(id, kind, name, base_url, username, api_key_encrypted, key_id, harbrr_url,
     enabled, created_at, updated_at)   UNIQUE(kind, base_url)
```

- One sealed secret, `api_key_encrypted`, AAD = **the app's own id** (discriminator
  `domain.AppSecret` == `"app"`). For API-key apps (qui, *arrs) `username` is empty and this
  holds the API key; for user+password apps (qBittorrent) `username` is set and this holds the
  password. "Credential" means whichever the kind uses. The discriminator is the same `"app"`
  the surface tables used for their per-row app credential, so the boot fold decrypts a legacy
  row under `(rowID, "app")` and re-seals under `(appID, "app")`.
- `harbrr_url` — "how THIS app reaches harbrr's feed" — moves here from both connection tables.
  It is a property of the app's vantage, identical across every push surface that uses the app.
  Download clients never read it (they receive torrents, they don't call back).
- `internal/apps.Service` owns get-or-create, decrypt, rotate, reference-counting, and the
  qui-instance proxy. It reuses `connresource.Lifecycle[domain.App]` (Minter nil — an App mints
  no harbrr key) for the insert-then-seal create.

### 2. Surface rows reference the App

`app_connections` and `announce_connections` gain `app_id` and, in the read/write paths, **stop
using** their own `api_key_encrypted`/`harbrr_url`; they keep their minted-harbrr-key fields
(`harbrr_api_key_id`/`harbrr_api_key_encrypted` — harbrr's own per-connection credential, not
app identity). `download_clients` gains `app_id` and stops using `username`/`secret_encrypted`;
`app_id` is NULL only for host-less kinds (blackhole — `drivers[kind].host == hostNone`),
enforced in `validate`. Per-row settings (sync level/scope/profile, instance id, categories)
stay on the surface row.

Legacy columns are **kept physically** (the `0015`/proxysplit precedent) — migration `0020`
only *adds* `app_id`. On create a surface still writes `base_url = app.BaseURL` (a non-secret)
so the existing `UNIQUE(kind, base_url)` index keeps meaning "one connection per app" without an
index change; the credential columns are left empty. A later cleanup migration drops the dead
columns and moves uniqueness onto `app_id` (see [Consequences](#consequences)).

### 3. Create accepts a reference OR inline identity (no seed endpoints)

Every surface's create accepts EITHER `appId` (reuse) OR inline `{baseUrl, username?, apiKey,
harbrrUrl?}`, which **get-or-creates** the App by `(kind, base_url)`. This is what makes "never
type twice" hold in every direction with zero seed endpoints: `GET /api/apps` is the universal
source lookup; the App-picker in each dialog reuses an existing App; inline is the first-time
path. #266's copy-seeds and its `qui-sources` reverse-seed endpoints are **not built** — the
registry supersedes them, and #72's `POST /api/app-connections/{id}/announce-target` seed
endpoint (plus `appsync.QuiSeed`) is **removed** rather than reworked; a "use as announce
target" affordance is now pure UI sugar that opens the announce-create dialog with the App
pre-picked.

**A typed credential is always authoritative.** Inline create against an already-existing
`(kind, base_url)`: if the caller typed a non-empty credential, it UPDATES the App's credential
(propagating, exactly like a rotation) — a user who affirmatively typed a key expects it used;
if the inline credential is empty (or the picker/`appId` path was used, where none is typed), the
stored credential is reused untouched. Changing a credential is therefore either an explicit App
PATCH (§4) or a non-empty inline entry.

### 4. Rotation propagates

`PATCH /api/apps/{id}` rotates the credential once; every referencing surface decrypts via the
App on its next call, so all three follow. This is the payoff over copy-seeds.

### 5. Secrets / AAD

The credential is sealed under the App's id (not a surface row's), so all three surfaces decrypt
through the App. Minted per-connection harbrr keys are unchanged (still AAD = the connection id).

### 6. Lifecycle

Deleting an App that is still referenced → **409** naming the referencing surfaces (block, not
cascade — a config convenience must never silently delete a working download client). Deleting a
surface row never deletes the App. Orphan Apps are listable and deletable. Single-user
self-hosted: no sharing/ACL — the App is a config convenience, not a tenancy boundary.

### 7. Migration (boot fold, `internal/resourcemigrate`)

A boot fold (`FoldApps`, guarded by an `app_meta` flag `apps_folded`, transactional, non-fatal,
retry-next-boot — the `resourcemigrate.Run` precedent) folds every legacy surface row into Apps:
decrypt the app-side secret (AAD = the row id), get-or-create the App by `(kind, base_url)`,
re-encrypt under the App's AAD, set `app_id`. Duplicate identities across surfaces reconcile
**newest-credential-wins, logged loudly** (processed newest-first; a later row whose decrypted
credential differs from the winner is logged — redacted, never the value). A row left unfolded
(fold failed) surfaces a clear typed `domain.ErrAppMigrationPending` error from its service on any
use path ("app migration pending — restart harbrr or check logs"), mapped to 503.

**There is exactly one read path — via `app_id`.** No legacy-column fallback: legacy and App
credentials live under different AAD (row-id vs app-id), so a `COALESCE` fallback would secretly
be a second decrypt path (the dual-path shim CLAUDE.md bans). Since the fold runs at boot before
serving and its only failure modes are DB-level (which break everything anyway), the pending path
is essentially unreachable in practice — kept as one path. The fold never logs a decrypted
credential.

## Consequences

- Rotation propagates; identity + credential stored once.
- Surface *update* dialogs lose base-URL/credential/harbrr-URL fields — those move to the App.
  Announce's PATCH becomes name-only; download's becomes name + settings.
- #266's copy-seed acceptance is met by the registry; the #72 seed endpoint and its
  `appsync.QuiSeed` helper are removed.
- The dead legacy columns (and the fold + `ErrAppMigrationPending` path) are removed in a
  follow-up cleanup migration, which also moves surface uniqueness onto `app_id`:
  **autobrr/harbrr#269**.
