// Package domain holds harbrr's persisted entity structs — plain data shared by
// the database repositories, the auth service, and the indexer registry. Keeping
// them in a neutral leaf package lets the storage layer return typed entities
// without coupling it to the business packages that consume them (the qui
// internal/domain pattern).
package domain

import "time"

// IndexerInstance is a configured tracker: a definition id plus user-chosen
// identity and base URL. The integer ID is internal and stable (it backs the
// encryption AAD of its secret settings); Slug is the stable user-facing
// identifier used as the Torznab {indexerId} path segment and the management
// resource id.
type IndexerInstance struct {
	ID           int64
	Slug         string
	DefinitionID string
	Name         string
	BaseURL      string
	Enabled      bool
	// Protocol is the acquisition protocol ("torrent" or "usenet"), derived from
	// the definition at Add time and immutable per instance. NOT NULL in the DB,
	// defaulting to "torrent".
	Protocol  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// User is harbrr's admin account. First-run setup creates exactly one. The
// password is stored only as an argon2id PHC hash — never recoverable.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// APIKey is a management/Torznab API key. Only its SHA-256 hash is stored; the
// plaintext is shown once at mint time. LastUsedAt is nil until first use.
type APIKey struct {
	ID         int64
	Name       string
	KeyHash    string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Health-event kinds — the four categories an indexer failure classifies
// into. Stored verbatim in indexer_health_events.kind.
const (
	HealthAuthFailure = "auth_failure"
	HealthRateLimited = "rate_limited"
	HealthParseError  = "parse_error"
	HealthAntiBot     = "anti_bot"
)

// IndexerHealthEvent is one recorded health signal for an instance: a classified
// failure with a credential-scrubbed detail and when it occurred. The table is
// append-only; an instance's API-surfaced status is derived from recent events.
type IndexerHealthEvent struct {
	ID         int64
	InstanceID int64
	Kind       string
	Detail     string
	OccurredAt time.Time
}

// IndexerSetting is one configured setting of an instance. A secret setting
// (IsSecret) stores its value in ValueEncrypted (base64 nonce‖ciphertext‖tag)
// with the KeyID that encrypted it and an empty Value; a plaintext setting stores
// Value and leaves ValueEncrypted/KeyID empty.
type IndexerSetting struct {
	Name           string
	Value          string
	ValueEncrypted string
	KeyID          string
	IsSecret       bool
}

// App-sync kinds — the *arr/qui targets harbrr can push indexer config into.
// Stored verbatim in app_connections.kind.
const (
	AppKindSonarr   = "sonarr"
	AppKindRadarr   = "radarr"
	AppKindLidarr   = "lidarr"
	AppKindReadarr  = "readarr"
	AppKindWhisparr = "whisparr"
	AppKindQui      = "qui"
)

// Sync levels — what reconciliation is allowed to do, set per connection (the
// Prowlarr "Sync Level" equivalent). Full mirrors harbrr exactly (add + update +
// remove orphans); AddUpdate never deletes (orphans are left for manual removal).
const (
	SyncLevelFull      = "full"
	SyncLevelAddUpdate = "add_update"
)

// Index scopes — which harbrr indexers a connection mirrors. All = every enabled
// instance; Selected = only the instances flagged in app_connection_indexers.
const (
	IndexScopeAll      = "all"
	IndexScopeSelected = "selected"
)

// Freeleech modes — which feed variant a connection is pushed, set per connection and
// defaulted by app kind (qui → bypass; *arrs → honor). Honor pushes the standard feed
// URL (the indexer's freeleech setting is respected); Bypass pushes the /full variant
// URL (the full catalog, for cross-seed consumers that must see every release).
const (
	FreeleechModeHonor  = "honor"
	FreeleechModeBypass = "bypass"
)

// Sync statuses — the outcome recorded on a connection (and per-indexer push).
const (
	SyncStatusOK      = "ok"
	SyncStatusPartial = "partial"
	SyncStatusError   = "error"
)

// AppConnection is a configured Sonarr/Radarr/qui app harbrr syncs its indexers
// into. Two secrets are stored encrypted (base64 nonce‖ciphertext‖tag) under KeyID,
// bound by the connection ID as encryption AAD: APIKeyEncrypted is the *app's* API
// key (so harbrr can call it), and HarbrrAPIKeyEncrypted is the plaintext of the
// dedicated harbrr key minted for this connection — persisted so every re-sync can
// re-push it into the app (api_keys stores only the hash). HarbrrAPIKeyID points at
// that minted key for revocation on delete. HarbrrURL is the base URL *this app*
// uses to reach harbrr's Torznab feed (it can differ per app on a Docker/LAN).
type AppConnection struct {
	ID                    int64
	Name                  string
	Kind                  string
	BaseURL               string
	APIKeyEncrypted       string
	HarbrrURL             string
	HarbrrAPIKeyID        int64
	HarbrrAPIKeyEncrypted string
	KeyID                 string
	Enabled               bool
	SyncLevel             string
	IndexScope            string
	FreeleechMode         string
	Priority              int
	LastSyncAt            *time.Time
	LastSyncStatus        string
	LastSyncError         string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Announce kinds — the cross-seed tools harbrr pushes new releases to. Stored verbatim in
// announce_connections.kind; validated in Go (no DB CHECK), so a new tool needs no
// migration (the #85 lesson).
const (
	AnnounceKindQui         = "qui"
	AnnounceKindCrossSeedV6 = "crossseed-v6"
)

// AnnounceConnection is a configured cross-seed tool harbrr pushes newly-seen releases to.
// Like AppConnection it stores two encrypted secrets under KeyID (AAD = the connection id):
// APIKeyEncrypted is the tool's own API key (so harbrr can call it), and
// HarbrrAPIKeyEncrypted is the plaintext of the dedicated minted harbrr key whose value
// signs the /dl link the tool fetches back. HarbrrAPIKeyID points at that key for revocation.
type AnnounceConnection struct {
	ID                    int64
	Name                  string
	Kind                  string
	BaseURL               string
	APIKeyEncrypted       string
	HarbrrURL             string
	HarbrrAPIKeyID        int64
	HarbrrAPIKeyEncrypted string
	KeyID                 string
	Enabled               bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// AppConnectionIndexer is the per-(connection, instance) sync ledger row — the
// authoritative reconciliation state. RemoteID is the id the target app assigned
// the pushed indexer (empty until the first successful push); PayloadHash is the
// hash of the last-pushed intent, so an unchanged indexer skips its update.
// Selected applies only when the connection's IndexScope is "selected".
type AppConnectionIndexer struct {
	ID             int64
	ConnectionID   int64
	InstanceID     int64
	RemoteID       string
	Selected       bool
	PayloadHash    string
	LastPushedAt   *time.Time
	LastPushStatus string
	LastPushError  string
}
