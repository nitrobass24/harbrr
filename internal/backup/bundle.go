// Package backup exports and restores harbrr's configuration + database as a single
// passphrase-encrypted bundle (backup / restore / migrate-to-a-new-host). The whole
// table payload is sealed under a passphrase-derived key (argon2id → AES-256-GCM), so a
// bundle is safe to store or move anywhere; only the passphrase opens it. Secrets are
// decrypted from the source's at-rest keyring at export time and re-sealed under the
// target's keyring at import time — DB secrets are AEAD-bound to their row id, so a
// restore that assigns fresh ids must rebind them (see restore.go).
package backup

import (
	"time"

	"github.com/autobrr/harbrr/internal/secrets"
)

// SchemaVersion is the bundle format version. Import refuses a version it does not know,
// so a newer bundle can't be silently half-restored onto an older harbrr.
const SchemaVersion = 1

// payloadAAD authenticates the sealed table payload against its format, so a blob can't
// be spliced into a different context. The passphrase salt is unique per export, so the
// derived key already differs per bundle; this pins the version too.
const payloadAAD = "harbrr-backup/v1"

// Envelope is the cleartext outer wrapper. Everything sensitive lives inside the sealed
// Payload; the cleartext fields carry only what an importer needs before it has the
// passphrase (the KDF + salt to derive the key) plus non-sensitive provenance.
type Envelope struct {
	SchemaVersion int                   `json:"schemaVersion"`
	HarbrrVersion string                `json:"harbrrVersion"`
	CreatedAt     string                `json:"createdAt"` // RFC3339, when the export ran
	KDF           secrets.PassphraseKDF `json:"kdf"`
	Salt          string                `json:"salt"`    // base64, the passphrase KDF salt
	Payload       string                `json:"payload"` // base64(AES-256-GCM(Tables JSON))
}

// Tables is the sealed inner payload: every backed-up table, with secrets in cleartext
// (the payload itself is encrypted). Row ids are the ORIGINAL source ids, kept only so
// import can remap cross-table foreign keys to the new ids it assigns.
type Tables struct {
	// Parents first (import order): rows the others reference by id.
	Proxies      []ProxyRow       `json:"proxies"`
	Solvers      []SolverRow      `json:"solvers"`
	SyncProfiles []SyncProfileRow `json:"syncProfiles"`
	APIKeys      []APIKeyRow      `json:"apiKeys"`
	// Then the rows that reference the parents above.
	IndexerInstances    []InstanceRow     `json:"indexerInstances"`
	AppConnections      []AppConnRow      `json:"appConnections"`
	AnnounceConnections []AnnounceConnRow `json:"announceConnections"`
	Notifications       []NotificationRow `json:"notifications"`
	// Standalone.
	AppSettings []AppSettingRow `json:"appSettings"`
	Admin       *UserRow        `json:"admin,omitempty"`
}

// ProxyRow carries the proxy's structured fields (host/port/username are plaintext;
// only the password is a secret) since #71 split the composite URL into structured
// storage. Password is decrypted at export and re-sealed under the target keyring on
// import.
type ProxyRow struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SolverRow carries the decrypted upstream URL (which embeds credentials); it is
// re-sealed under the target keyring on import.

type SolverRow struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	URL        string    `json:"url"`
	MaxTimeout int       `json:"maxTimeout"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// SyncProfileRow has no secrets.
type SyncProfileRow struct {
	ID                      int64     `json:"id"`
	Name                    string    `json:"name"`
	Categories              []int     `json:"categories"`
	MinSeeders              int       `json:"minSeeders"`
	EnableRss               bool      `json:"enableRss"`
	EnableAutomaticSearch   bool      `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool      `json:"enableInteractiveSearch"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// APIKeyRow carries the one-way key hash verbatim (there is no plaintext to recover), so
// a migrated *arr keeps authenticating against harbrr's feed.
type APIKeyRow struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"keyHash"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// SettingRow is one indexer setting. Value is always plaintext here (a secret setting is
// decrypted at export and re-sealed at import under the new instance id).
type SettingRow struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	IsSecret bool   `json:"isSecret"`
}

// InstanceRow is an indexer instance plus its settings. ProxyID/SolverID are ORIGINAL ids
// remapped to the target's new proxy/solver ids on import.
type InstanceRow struct {
	ID           int64        `json:"id"`
	Slug         string       `json:"slug"`
	DefinitionID string       `json:"definitionId"`
	Name         string       `json:"name"`
	BaseURL      string       `json:"baseUrl"`
	Enabled      bool         `json:"enabled"`
	Protocol     string       `json:"protocol"`
	ProxyID      *int64       `json:"proxyId,omitempty"`
	SolverID     *int64       `json:"solverId,omitempty"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
	Settings     []SettingRow `json:"settings"`
}

// AppConnRow carries both decrypted secrets (the app's own key + the minted harbrr key)
// and the original api-key / sync-profile references, remapped on import. Transient sync
// status (last_sync_*) and the reconciliation ledger are intentionally not carried — they
// are derived state, rebuilt on the next sync. The one exception is SelectedInstanceIDs:
// the ledger's `selected` flags are user intent (which indexers a scope="selected"
// connection mirrors), not derived, so those original instance ids ride along and are
// remapped to the target's new instance ids on import.
type AppConnRow struct {
	ID                  int64     `json:"id"`
	Name                string    `json:"name"`
	Kind                string    `json:"kind"`
	BaseURL             string    `json:"baseUrl"`
	APIKey              string    `json:"apiKey"`
	HarbrrURL           string    `json:"harbrrUrl"`
	HarbrrAPIKeyID      *int64    `json:"harbrrApiKeyId,omitempty"`
	HarbrrAPIKey        string    `json:"harbrrApiKey"`
	Enabled             bool      `json:"enabled"`
	SyncLevel           string    `json:"syncLevel"`
	IndexScope          string    `json:"indexScope"`
	FreeleechMode       string    `json:"freeleechMode"`
	Priority            int       `json:"priority"`
	SyncProfileID       *int64    `json:"syncProfileId,omitempty"`
	SelectedInstanceIDs []int64   `json:"selectedInstanceIds,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// AnnounceConnRow mirrors AppConnRow's secret pair for cross-seed announce targets.
type AnnounceConnRow struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	BaseURL        string    `json:"baseUrl"`
	APIKey         string    `json:"apiKey"`
	HarbrrURL      string    `json:"harbrrUrl"`
	HarbrrAPIKeyID *int64    `json:"harbrrApiKeyId,omitempty"`
	HarbrrAPIKey   string    `json:"harbrrApiKey"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// NotificationRow carries the decrypted destination URL (webhook/Discord), re-sealed on
// import.
type NotificationRow struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	URL             string    `json:"url"`
	Enabled         bool      `json:"enabled"`
	OnHealthFailure bool      `json:"onHealthFailure"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// AppSettingRow is one runtime config key/value (never holds a secret by design).
type AppSettingRow struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// UserRow is the single admin. PasswordHash is a one-way argon2id hash, carried verbatim
// so the operator's login survives a migrate; import overwrites the target's bootstrap
// admin with it.
type UserRow struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
