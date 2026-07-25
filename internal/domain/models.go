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
// identifier used as the Torznab {slug} path segment and the management
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
	Protocol string
	// ProxyID / SolverID reference the global proxy / anti-bot-solver resources
	// this instance uses, or nil for none. The engine resolves them into the
	// per-request config at build time (registry.buildAdapter); ON DELETE SET NULL
	// means deleting a resource just drops the reference.
	ProxyID  *int64
	SolverID *int64
	// Priority is the Servarr indexer priority (1-50, 1 = highest, default 25),
	// pushed per indexer by appsync (Prowlarr semantics).
	Priority int
	// MinSeeders is the per-indexer minimum-seeders floor (torrent-only, pushed as
	// minimumSeeders); 0 = unset, not pushed. The sole source since #365 — a sync
	// profile no longer carries a fallback value.
	MinSeeders int
	// EnableRss / EnableAutomaticSearch / EnableInteractiveSearch are the per-search-mode
	// flags pushed into a Servarr indexer registration (#365 moved these off the sync
	// profile onto the instance so behavior can differ per indexer); each is ANDed with
	// Enabled at push time (a disabled instance forces every flag false). Default true.
	EnableRss               bool
	EnableAutomaticSearch   bool
	EnableInteractiveSearch bool
	// SyncCategories narrows the Newznab categories this indexer pushes, within the
	// consuming app's own content type (never beyond it); empty means no narrowing (the
	// full app-gated set is pushed) — the same parent-block convention the old profile
	// category picker used.
	SyncCategories []int
	CreatedAt      time.Time
	UpdatedAt      time.Time
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

// Health-event kinds — the categories an indexer failure classifies into.
// Stored verbatim in indexer_health_events.kind.
const (
	HealthAuthFailure = "auth_failure"
	HealthRateLimited = "rate_limited"
	HealthParseError  = "parse_error"
	HealthAntiBot     = "anti_bot"
	// HealthTransport covers transport-level failures — connection refused/reset,
	// TLS/DNS failures, client timeouts, EOF-after-200 reads — that leave a tracker
	// unreachable rather than reachable-but-unhappy. One coarse kind; the event
	// detail string carries the specifics (#223).
	HealthTransport = "transport"
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

// AppSecret is the AAD "setting" discriminator binding an App's one encrypted
// credential (api_key_encrypted — an API key OR a password, depending on kind) to
// the App's own id. It is the string "app" — the same discriminator the surface
// tables used for their per-row app credential before the App registry (so the boot
// fold decrypts a legacy row under (rowID, "app") and re-seals under (appID, "app")).
const AppSecret = "app"

// App is a first-class external service harbrr connects to — a (kind, base_url)
// identity plus one sealed credential and the app's vantage onto harbrr — stored
// ONCE and referenced by the three surface tables (app_connections,
// announce_connections, download_clients) via app_id. APIKeyEncrypted holds the
// credential (an API key for qui/*arrs where Username is empty; a password for
// user+password kinds where Username is set), sealed under KeyID with the App's own
// id as AAD (discriminator AppSecret). HarbrrURL is how THIS app reaches harbrr's
// feed — identical across every push surface that uses the App; download clients
// never read it. UNIQUE(kind, base_url).
type App struct {
	ID              int64
	Kind            string
	Name            string
	BaseURL         string
	Username        string
	APIKeyEncrypted string
	KeyID           string
	HarbrrURL       string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Sync levels — what reconciliation is allowed to do, set per connection (the
// Prowlarr "Sync Level" equivalent). Full mirrors harbrr exactly (add + update +
// remove orphans); AddUpdate never deletes (orphans are left for manual removal).
const (
	SyncLevelFull      = "full"
	SyncLevelAddUpdate = "add_update"
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

// SyncProfile is a named, reusable ROUTING set (#365) a connection references by id (the
// Prowlarr "Sync Profile" equivalent, narrowed to just routing — all sync behavior now
// lives per-indexer on IndexerInstance). IndexerIDs names the indexer instances this
// profile routes a connection to; empty means every compatible indexer (mirrors the
// empty-categories convention and avoids a profile edit silently narrowing a connection
// to nothing). No secrets live here.
type SyncProfile struct {
	ID         int64
	Name       string
	IndexerIDs []int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AppConnection is a configured Sonarr/Radarr/qui app harbrr syncs its indexers into.
// The app's own API key lives on the referenced App (AppID), not here. The one secret
// stored on this row is HarbrrAPIKeyEncrypted: the plaintext of the dedicated harbrr key
// minted for this connection (base64 nonce‖ciphertext‖tag under KeyID, bound by the
// connection ID as encryption AAD) — persisted so every re-sync can re-push it into the
// app (api_keys stores only the hash). HarbrrAPIKeyID points at that minted key for
// revocation on delete. HarbrrURL is the base URL *this app* uses to reach harbrr's
// Torznab feed (it can differ per app on a Docker/LAN) — populated from the App, see AppID.
type AppConnection struct {
	ID   int64
	Name string
	Kind string
	// AppID references the App holding this connection's identity + credential
	// (base_url, api key, harbrr_url); always set (the migration that dropped the
	// legacy columns refuses to apply while any non-hostless row is still NULL).
	// BaseURL/HarbrrURL are populated by the service from the App on read — they have
	// no backing column on this row (dropped by #269; the App is the sole store).
	AppID                 *int64
	BaseURL               string
	HarbrrURL             string
	HarbrrAPIKeyID        int64
	HarbrrAPIKeyEncrypted string
	KeyID                 string
	Enabled               bool
	SyncLevel             string
	FreeleechMode         string
	// SyncProfileID references the sync profile (routing set) this connection uses, or
	// nil for none — nil, or a profile with an empty selection, means every compatible
	// indexer. ON DELETE SET NULL means deleting a profile just drops the reference, but
	// the appsync service refuses the delete while any connection still references it
	// (see appsync.DeleteProfile) — the FK is a defensive backstop, not the guard.
	SyncProfileID  *int64
	LastSyncAt     *time.Time
	LastSyncStatus string
	LastSyncError  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Announce kinds — the cross-seed tools harbrr pushes new releases to. Stored verbatim in
// announce_connections.kind; validated in Go (no DB CHECK), so a new tool needs no
// migration (the #85 lesson).
const (
	AnnounceKindQui         = "qui"
	AnnounceKindCrossSeedV6 = "crossseed-v6"
)

// Notification types — the pluggable senders a notification target dispatches
// through. Stored verbatim in notifications.type; validated in Go (no DB CHECK), so
// a new sender needs no migration (the #85 lesson).
const (
	NotifyTypeWebhook = "webhook"
	NotifyTypeDiscord = "discord"
)

// Notification is a configured notification target harbrr fires operational events
// at (indexer health failures today). The destination — a generic webhook URL or a
// Discord webhook URL, either of which may embed a secret token — is stored
// encrypted (base64 nonce‖ciphertext‖tag) under KeyID, bound by the notification id
// as encryption AAD, exactly like a connection's secret. OnHealthFailure gates the
// health-event trigger per target.
type Notification struct {
	ID              int64
	Name            string
	Type            string
	URLEncrypted    string
	KeyID           string
	Enabled         bool
	OnHealthFailure bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Proxy scheme types — stored verbatim in proxies.type, validated in Go (no DB
// CHECK) so a new scheme needs no migration. These mirror the inline proxy_type
// values buildTransport already accepts.
const (
	ProxyTypeHTTP    = "http"
	ProxyTypeHTTPS   = "https"
	ProxyTypeSOCKS5  = "socks5"
	ProxyTypeSOCKS5H = "socks5h"
)

// SolverTypeFlaresolverr is the only global anti-bot-solver type today (the
// manual-cookie solver stays inline per-tracker). Stored in solvers.type;
// validated in Go so a future solver kind needs no migration.
const SolverTypeFlaresolverr = "flaresolverr"

// FlareMaxTimeoutCapSeconds is the upper bound (in seconds) on a solver's
// per-solve FlareSolverr budget. It is the SINGLE SOURCE OF TRUTH for the cap:
// the solver service rejects a MaxTimeout above it at save time, and the login
// stage derives its flareMaxTimeoutCap from it (an out-of-range budget resets to
// the 60s default). Keeping one const stops those two checks from drifting.
const FlareMaxTimeoutCapSeconds = 180

// ProxySecretPassword / SolverSecretURL are the AAD "setting" discriminators
// binding each resource's encrypted secret to its own row id (mirroring notify's
// secretURL). Shared so the management service encrypts and the engine decrypts
// under the same name. ProxySecretPassword is only the proxy's password (#71 split
// the rest of the proxy — host/port/username — into plain structured fields);
// SolverSecretURL is still the solver's full endpoint URL, its secret in whole.
const (
	ProxySecretPassword = "proxy_password" //nolint:gosec // G101: an AAD "setting" discriminator name, not a credential.
	SolverSecretURL     = "solver_url"     //nolint:gosec // G101: an AAD "setting" discriminator name, not a credential.
)

// Proxy is a global, reusable proxy an indexer instance references by id. Host,
// Port, and Username are plain (visible on read, never masked); Password is the
// only stored secret, encrypted under KeyID with the proxy's own id as AAD. The
// transport URL (type://[user[:pass]@]host:port) is composed where the proxy is
// applied (internal/indexer/registry) and never stored.
type Proxy struct {
	ID                int64
	Name              string
	Type              string
	Host              string
	Port              int
	Username          string
	PasswordEncrypted string
	KeyID             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Solver is a global, reusable anti-bot solver an indexer instance references by
// id (FlareSolverr today). The endpoint URL is stored encrypted like Proxy's;
// MaxTimeout is the FlareSolverr per-solve budget in seconds (0 = the solver's
// default).
type Solver struct {
	ID           int64
	Name         string
	Type         string
	URLEncrypted string
	KeyID        string
	MaxTimeout   int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AnnounceConnection is a configured cross-seed tool harbrr pushes newly-seen releases to.
// The tool's own API key lives on the referenced App (AppID), not here. Like AppConnection,
// the one secret stored on this row is HarbrrAPIKeyEncrypted under KeyID (AAD = the
// connection id): the plaintext of the dedicated minted harbrr key whose value signs the
// /dl link the tool fetches back. HarbrrAPIKeyID points at that key for revocation.
type AnnounceConnection struct {
	ID   int64
	Name string
	Kind string
	// AppID references the App holding this connection's identity + credential; see
	// AppConnection.AppID. BaseURL/HarbrrURL are populated from the App on read.
	AppID                 *int64
	BaseURL               string
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
// hash of the last-pushed intent, so an unchanged indexer skips its update. It is a
// pure reconcile ledger (#365 removed Selected — which indexers a connection syncs is
// now the referenced sync profile's IndexerIDs, not a per-row flag here).
type AppConnectionIndexer struct {
	ID             int64
	ConnectionID   int64
	InstanceID     int64
	RemoteID       string
	PayloadHash    string
	LastPushedAt   *time.Time
	LastPushStatus string
	LastPushError  string
}

// Download-client kinds — stored verbatim in download_clients.kind, validated in Go
// (no DB CHECK, the #85 lesson) so adding a kind needs no migration. All ten are
// seeded here up front; a kind is only *creatable* once its driver registers in
// internal/download's factory map — until then Service.Create rejects it as
// unregistered (domain.ErrInvalid). See autobrr/harbrr#8 for the sub-issues that
// register the remaining nine.
const (
	DownloadClientKindQBittorrent     = "qbittorrent"
	DownloadClientKindSabnzbd         = "sabnzbd"
	DownloadClientKindNZBGet          = "nzbget"
	DownloadClientKindQui             = "qui"
	DownloadClientKindFlood           = "flood"
	DownloadClientKindDownloadStation = "download-station"
	DownloadClientKindTransmission    = "transmission"
	DownloadClientKindDeluge          = "deluge"
	DownloadClientKindRTorrent        = "rtorrent"
	DownloadClientKindBlackhole       = "blackhole"
)

// DownloadClientSecret is the AAD "setting" discriminator binding a download
// client's encrypted secret (password/API key, meaning depends on kind) to its own
// row id, mirroring notify's secretURL / proxy's ProxySecretPassword.
const DownloadClientSecret = "download_client_secret" //nolint:gosec // G101: an AAD discriminator name, not a credential.

// QBittorrentSettings holds the qBittorrent-specific per-client options. All
// fields are optional (zero value = client default / unset).
type QBittorrentSettings struct {
	Category      string   `json:"category,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	StartPaused   bool     `json:"startPaused,omitempty"`
	TLSSkipVerify bool     `json:"tlsSkipVerify,omitempty"`
}

// TransmissionSettings holds the Transmission-specific per-client options.
type TransmissionSettings struct {
	DownloadDir string `json:"downloadDir,omitempty"`
	StartPaused bool   `json:"startPaused,omitempty"`
}

// DelugeSettings holds the Deluge-specific per-client options. V1 selects the
// legacy 1.3 daemon protocol; the default is the v2 daemon.
type DelugeSettings struct {
	V1          bool   `json:"v1,omitempty"`
	Label       string `json:"label,omitempty"`
	DownloadDir string `json:"downloadDir,omitempty"`
	StartPaused bool   `json:"startPaused,omitempty"`
}

// RTorrentSettings holds the rTorrent-specific per-client options.
type RTorrentSettings struct {
	Label         string `json:"label,omitempty"`
	Directory     string `json:"directory,omitempty"`
	StartPaused   bool   `json:"startPaused,omitempty"`
	TLSSkipVerify bool   `json:"tlsSkipVerify,omitempty"`
}

// QuiSettings holds the qui-specific per-client options. qui (autobrr/qui) is a
// multi-instance qBittorrent manager keyed by int instance id — InstanceID is the
// only required field (validated > 0 by the download service).
type QuiSettings struct {
	InstanceID  int      `json:"instanceId"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	StartPaused bool     `json:"startPaused,omitempty"`
}

// FloodSettings holds the Flood-specific per-client options. Flood has no category
// concept, so a caller's AddOptions.Category is folded into Tags by the driver.
type FloodSettings struct {
	Destination string   `json:"destination,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	StartPaused bool     `json:"startPaused,omitempty"`
}

// DownloadStationSettings holds the Synology Download Station-specific per-client
// option: a destination folder relative to a shared folder (no leading slash).
type DownloadStationSettings struct {
	Directory string `json:"directory,omitempty"`
}

// SabnzbdSettings holds the SABnzbd-specific per-client options: the default
// category an Add falls back to when the caller doesn't supply one.
type SabnzbdSettings struct {
	Category string `json:"category,omitempty"`
}

// NZBGetSettings holds the NZBGet-specific per-client options: the default
// category an Add falls back to when the caller doesn't supply one.
type NZBGetSettings struct {
	Category string `json:"category,omitempty"`
}

// BlackholeSettings holds the blackhole driver's watch-folder configuration: the
// resolved release is written as a complete file into TorrentDir/NZBDir for a
// real client to pick up. At least one dir must be set (validated by the
// download service, since only it knows the row's Kind); SaveMagnetFiles opts
// into writing a magnet-only release as a <name>.magnet link file — without it,
// Add fails rather than silently dropping the release.
type BlackholeSettings struct {
	TorrentDir      string `json:"torrentDir,omitempty"`
	NZBDir          string `json:"nzbDir,omitempty"`
	SaveMagnetFiles bool   `json:"saveMagnetFiles,omitempty"`
}

// DownloadClientSettings is the typed wrapper persisted (marshalled) into
// download_clients.settings_json: one pointer field per kind, never a bare
// map[string]any. Exactly one field may be populated, and it must match the
// owning row's Kind — a mismatch is domain.ErrInvalid (checked by the download
// service, since only it knows the row's Kind).
type DownloadClientSettings struct {
	QBittorrent     *QBittorrentSettings     `json:"qbittorrent,omitempty"`
	Blackhole       *BlackholeSettings       `json:"blackhole,omitempty"`
	Sabnzbd         *SabnzbdSettings         `json:"sabnzbd,omitempty"`
	NZBGet          *NZBGetSettings          `json:"nzbget,omitempty"`
	Qui             *QuiSettings             `json:"qui,omitempty"`
	Flood           *FloodSettings           `json:"flood,omitempty"`
	DownloadStation *DownloadStationSettings `json:"downloadStation,omitempty"`
	Transmission    *TransmissionSettings    `json:"transmission,omitempty"`
	Deluge          *DelugeSettings          `json:"deluge,omitempty"`
	RTorrent        *RTorrentSettings        `json:"rtorrent,omitempty"`
}

// DownloadClient is a configured download client harbrr can send grabbed releases to.
// A networked kind's host/username/credential live on the referenced App (AppID); this
// row seals no credential of its own (KeyID is unused going forward — kept only because
// nothing forces its removal, see #269). Settings holds kind-specific options (see
// DownloadClientSettings).
type DownloadClient struct {
	ID   int64
	Name string
	Kind string
	// AppID references the App holding this client's identity + credential (host,
	// username, secret). NULL only for host-less kinds (blackhole), which have no
	// network endpoint or credential of their own. Host/Username are populated from
	// the App on read — they have no backing column on this row (dropped by #269).
	AppID     *int64
	Enabled   bool
	Host      string
	Username  string
	KeyID     string
	Settings  DownloadClientSettings
	CreatedAt time.Time
	UpdatedAt time.Time
}
