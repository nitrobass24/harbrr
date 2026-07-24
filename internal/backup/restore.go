package backup

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// idMap maps a source row id to the id the target assigned on re-insert, for remapping
// cross-table foreign keys (the AAD rebind means ids can't be preserved verbatim).
type idMap map[int64]int64

// remap resolves an optional source FK to the target's new id: nil stays nil, and a
// reference with no mapping (a dangling id) collapses to nil — the column's ON DELETE SET
// NULL intent — rather than a foreign-key fault.
func (m idMap) remap(old *int64) *int64 {
	if old == nil {
		return nil
	}
	if n, ok := m[*old]; ok {
		return &n
	}
	return nil
}

// configTables are the resource tables whose presence means "already configured". The
// bootstrap admin and app_settings defaults don't count, so a fresh-setup instance
// imports without force (the migrate flow).
var configTables = []string{
	"indexer_instances", "app_connections", "announce_connections",
	"proxies", "solvers", "notifications", "sync_profiles",
}

// wipeOrder deletes referencing tables before the tables they reference (foreign_keys is
// ON). Deleting indexer_instances / app_connections cascades their child rows
// (indexer_settings, app_connection_indexers).
var wipeOrder = []string{
	"app_connections", "announce_connections", "indexer_instances", "notifications",
	"proxies", "solvers", "sync_profiles", "api_keys", "app_settings",
}

// restore applies a decoded bundle as a transactional wipe-and-load: refuse a configured
// instance unless force, resolve every app-sync/announce connection's App, wipe the
// backed-up tables, then re-insert everything, re-sealing each secret under the target
// keyring with the new row id and remapping foreign keys.
//
// App resolution (resolveConnApps) MUST run before the transaction opens: db.go configures
// the underlying *sql.DB with SetMaxOpenConns(1) (a single physical connection), and
// apps.Service.Resolve always runs against its own s.db handle, never the caller's tx (ADR
// 0004 §6 — an orphan App from a later-failing create is an accepted risk, exactly the risk
// accepted here too). Calling Resolve from inside this tx would try to check out that same
// single connection while the tx already holds it, deadlocking the pool. The force-guard
// check runs before resolution too (on s.db, not tx-scoped), so a rejected import creates or
// rotates no App as a side effect — the small check-then-wipe TOCTOU window this opens is
// accepted for single-user self-hosted software (CLAUDE.md).
func (s *Service) restore(ctx context.Context, t *Tables, force bool) error {
	if err := ensureRestorable(ctx, s.db, force, t.Admin != nil); err != nil {
		return err
	}
	appConnApps, err := s.resolveAppConnApps(ctx, t.AppConnections)
	if err != nil {
		return err
	}
	announceConnApps, err := s.resolveAnnounceConnApps(ctx, t.AnnounceConnections)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("backup: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The admin is replaced only when the bundle carries one, so a bundle from a
	// fresh (pre-setup) instance can't lock the operator out of the target.
	if err := wipe(ctx, tx, t.Admin != nil); err != nil {
		return err
	}
	if err := s.load(ctx, tx, t, appConnApps, announceConnApps); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backup: commit restore: %w", err)
	}
	return nil
}

// resolveAppConnApps / resolveAnnounceConnApps get-or-create the App each bundled
// connection references (see resolveConnAppForLoad), keyed by the row's ORIGINAL (source)
// id — loadAppConnections/loadAnnounceConnections look up the pre-resolved App id by that
// key instead of calling Resolve themselves (see restore's doc comment for why).
func (s *Service) resolveAppConnApps(ctx context.Context, rows []AppConnRow) (idMap, error) {
	out := make(idMap, len(rows))
	for _, r := range rows {
		app, err := s.resolveConnAppForLoad(ctx, r.Kind, r.Name, r.BaseURL, r.APIKey, r.HarbrrURL)
		if err != nil {
			return nil, err
		}
		out[r.ID] = app.ID
	}
	return out, nil
}

func (s *Service) resolveAnnounceConnApps(ctx context.Context, rows []AnnounceConnRow) (idMap, error) {
	out := make(idMap, len(rows))
	for _, r := range rows {
		app, err := s.resolveConnAppForLoad(ctx, r.Kind, r.Name, r.BaseURL, r.APIKey, r.HarbrrURL)
		if err != nil {
			return nil, err
		}
		out[r.ID] = app.ID
	}
	return out, nil
}

// ensureRestorable refuses to overwrite existing state unless force is set: any configured
// resource, or — because a bundle carrying an admin replaces the target's login — an
// existing admin user. A truly-empty instance imports freely, but one that has completed
// first-run setup must opt in before its admin is swapped (the import is authenticated, so
// there is always an admin to protect once setup is done).
func ensureRestorable(ctx context.Context, q dbinterface.Execer, force, bundleHasAdmin bool) error {
	if force {
		return nil
	}
	for _, table := range configTables {
		n, err := countRows(ctx, q, table)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s already has %d row(s) — pass force to overwrite", ErrConflict, table, n)
		}
	}
	if bundleHasAdmin {
		n, err := countRows(ctx, q, "users")
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: importing this bundle would replace the admin login — pass force to overwrite", ErrConflict)
		}
	}
	return nil
}

func wipe(ctx context.Context, q dbinterface.Execer, includeUsers bool) error {
	tables := wipeOrder
	if includeUsers {
		tables = append(append([]string{}, wipeOrder...), "users")
	}
	for _, table := range tables {
		if _, err := q.ExecContext(ctx, q.Rebind("DELETE FROM "+table)); err != nil {
			return fmt.Errorf("backup: wipe %s: %w", table, err)
		}
	}
	return nil
}

// load re-inserts every table in foreign-key order (parents first), threading each
// parent's source→target id map into the children that reference it. appConnApps/
// announceConnApps are the pre-resolved (source row id -> App id) maps built before this
// transaction opened (see restore's doc comment).
func (s *Service) load(ctx context.Context, q dbinterface.Execer, t *Tables, appConnApps, announceConnApps idMap) error {
	proxyIDs, err := s.loadProxies(ctx, q, t.Proxies)
	if err != nil {
		return err
	}
	solverIDs, err := s.loadSolvers(ctx, q, t.Solvers)
	if err != nil {
		return err
	}
	profileIDs, err := loadSyncProfiles(ctx, q, t.SyncProfiles)
	if err != nil {
		return err
	}
	apiKeyIDs, err := loadAPIKeys(ctx, q, t.APIKeys)
	if err != nil {
		return err
	}
	instanceIDs, err := s.loadInstances(ctx, q, t.IndexerInstances, proxyIDs, solverIDs)
	if err != nil {
		return err
	}
	if err := s.loadAppConnections(ctx, q, t.AppConnections, appConnApps, apiKeyIDs, profileIDs, instanceIDs); err != nil {
		return err
	}
	if err := s.loadAnnounceConnections(ctx, q, t.AnnounceConnections, announceConnApps, apiKeyIDs); err != nil {
		return err
	}
	if err := s.loadNotifications(ctx, q, t.Notifications); err != nil {
		return err
	}
	if err := loadAppSettings(ctx, q, t.AppSettings); err != nil {
		return err
	}
	return loadAdmin(ctx, q, t.Admin)
}

func (s *Service) loadProxies(ctx context.Context, q dbinterface.Execer, rows []ProxyRow) (idMap, error) {
	repo := database.Proxies{}
	m := make(idMap, len(rows))
	for _, r := range rows {
		newID, err := repo.InsertProxy(ctx, q, domain.Proxy{
			Name: r.Name, Type: r.Type, Host: r.Host, Port: r.Port, Username: r.Username,
			PasswordEncrypted: "", KeyID: s.keyring.KeyID(),
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: insert proxy %q: %w", r.Name, err)
		}
		if err := s.sealSecret(ctx, q, newID, domain.ProxySecretPassword, r.Password, "proxy", repo.SetProxySecret); err != nil {
			return nil, err
		}
		m[r.ID] = newID
	}
	return m, nil
}

// sealSecret encrypts plaintext under (id, disc) via connresource.Seal and writes the
// ciphertext through set (each resource's SetXSecret, which share the (ctx, q, id, enc,
// keyID) shape); label names the resource in any error. Shared by the single-secret
// loaders (proxies, solvers, notifications); connection rows' harbrr key uses
// sealHarbrrKey (the app/tool credential moved onto its own App row — see
// resolveConnAppForLoad — so only one secret is left per connection row).
func (s *Service) sealSecret(ctx context.Context, q dbinterface.Execer, id int64, disc, plaintext, label string,
	set func(ctx context.Context, q dbinterface.Execer, id int64, enc, keyID string) error,
) error {
	encrypted, keyID, err := connresource.Seal(s.keyring, id, []connresource.Secret{{Discriminator: disc, Plaintext: plaintext}})
	if err != nil {
		return fmt.Errorf("backup: seal %s secret: %w", label, err)
	}
	if err := set(ctx, q, id, encrypted[0], keyID); err != nil {
		return fmt.Errorf("backup: set %s secret: %w", label, err)
	}
	return nil
}

func (s *Service) loadSolvers(ctx context.Context, q dbinterface.Execer, rows []SolverRow) (idMap, error) {
	repo := database.Solvers{}
	m := make(idMap, len(rows))
	for _, r := range rows {
		newID, err := repo.InsertSolver(ctx, q, domain.Solver{
			Name: r.Name, Type: r.Type, URLEncrypted: "", KeyID: s.keyring.KeyID(),
			MaxTimeout: r.MaxTimeout, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: insert solver %q: %w", r.Name, err)
		}
		if err := s.sealSecret(ctx, q, newID, domain.SolverSecretURL, r.URL, "solver", repo.SetSolverSecret); err != nil {
			return nil, err
		}
		m[r.ID] = newID
	}
	return m, nil
}

func loadSyncProfiles(ctx context.Context, q dbinterface.Execer, rows []SyncProfileRow) (idMap, error) {
	repo := database.SyncProfiles{}
	m := make(idMap, len(rows))
	for _, r := range rows {
		newID, err := repo.InsertProfile(ctx, q, domain.SyncProfile{
			Name: r.Name, Categories: r.Categories, MinSeeders: r.MinSeeders,
			EnableRss: r.EnableRss, EnableAutomaticSearch: r.EnableAutomaticSearch,
			EnableInteractiveSearch: r.EnableInteractiveSearch,
			CreatedAt:               r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: insert sync profile %q: %w", r.Name, err)
		}
		m[r.ID] = newID
	}
	return m, nil
}

// loadAPIKeys re-inserts via raw SQL to preserve created_at + last_used_at (the repo's
// Create drops the latter) and to capture the new id for FK remapping.
func loadAPIKeys(ctx context.Context, q dbinterface.Execer, rows []APIKeyRow) (idMap, error) {
	m := make(idMap, len(rows))
	for _, r := range rows {
		res, err := q.ExecContext(ctx,
			q.Rebind(`INSERT INTO api_keys (name, key_hash, created_at, last_used_at) VALUES (?, ?, ?, ?)`),
			r.Name, r.KeyHash, r.CreatedAt.UTC().Format(time.RFC3339), nullTime(r.LastUsedAt))
		if err != nil {
			return nil, fmt.Errorf("backup: insert api key %q: %w", r.Name, err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("backup: api key last insert id: %w", err)
		}
		m[r.ID] = newID
	}
	return m, nil
}

// restoreDefaultPriority is the Servarr indexer priority (Prowlarr semantics) a
// restored instance gets when its bundle predates the priority field (#364):
// a bundle written before then carries the JSON zero value (0), which must not
// restore as priority 0 (the fleet would then re-push every indexer to the apps at
// an invalid priority) — it defaults to the same value a live Add without one gets.
const restoreDefaultPriority = 25

func (s *Service) loadInstances(ctx context.Context, q dbinterface.Execer, rows []InstanceRow, proxyIDs, solverIDs idMap) (idMap, error) {
	repo := database.Instances{}
	m := make(idMap, len(rows))
	for _, r := range rows {
		priority := r.Priority
		if priority == 0 {
			priority = restoreDefaultPriority
		}
		newID, err := repo.Insert(ctx, q, domain.IndexerInstance{
			Slug: r.Slug, DefinitionID: r.DefinitionID, Name: r.Name, BaseURL: r.BaseURL,
			Enabled: r.Enabled, Protocol: r.Protocol,
			ProxyID: proxyIDs.remap(r.ProxyID), SolverID: solverIDs.remap(r.SolverID),
			Priority: priority, MinSeeders: r.MinSeeders,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: insert indexer %q: %w", r.Slug, err)
		}
		if err := s.loadSettings(ctx, q, newID, r.Settings); err != nil {
			return nil, err
		}
		m[r.ID] = newID
	}
	return m, nil
}

func (s *Service) loadSettings(ctx context.Context, q dbinterface.Execer, instanceID int64, settings []SettingRow) error {
	repo := database.Instances{}
	for _, st := range settings {
		row := domain.IndexerSetting{Name: st.Name, IsSecret: st.IsSecret}
		if st.IsSecret {
			encrypted, keyID, err := connresource.Seal(s.keyring, instanceID, []connresource.Secret{{Discriminator: st.Name, Plaintext: st.Value}})
			if err != nil {
				return fmt.Errorf("backup: seal setting %q: %w", st.Name, err)
			}
			row.ValueEncrypted, row.KeyID = encrypted[0], keyID
		} else {
			row.Value = st.Value
		}
		if err := repo.InsertSetting(ctx, q, instanceID, row); err != nil {
			return fmt.Errorf("backup: insert setting %q: %w", st.Name, err)
		}
	}
	return nil
}

// resolveConnAppForLoad get-or-creates the App a bundled connection references (by
// (kind, base_url), exactly like a live create — see apps.Service.Resolve), sealing/
// rotating its credential under the App's own id. Two connections carrying the same
// (kind, base_url) — e.g. an app-sync and an announce connection both against the same
// qui instance — resolve to the SAME App rather than minting a duplicate, mirroring the
// live create paths' dedup. Called only from the pre-transaction resolve pass (see
// restore's doc comment) — never while the wipe/load tx is open.
func (s *Service) resolveConnAppForLoad(ctx context.Context, kind, name, baseURL, apiKey, harbrrURL string) (domain.App, error) {
	app, err := s.apps.Resolve(ctx, apps.Ref{Kind: kind, Name: name, BaseURL: baseURL, APIKey: apiKey, HarbrrURL: harbrrURL})
	if err != nil {
		return domain.App{}, fmt.Errorf("backup: resolve app for %q: %w", name, err)
	}
	return app, nil
}

func (s *Service) loadAppConnections(ctx context.Context, q dbinterface.Execer, rows []AppConnRow, appConnApps, apiKeyIDs, profileIDs, instanceIDs idMap) error {
	repo := database.AppConnections{}
	for _, r := range rows {
		appID := appConnApps[r.ID]
		newID, err := repo.InsertConnection(ctx, q, domain.AppConnection{
			Name: r.Name, Kind: r.Kind, AppID: &appID,
			HarbrrAPIKeyID: zeroIfNil(apiKeyIDs.remap(r.HarbrrAPIKeyID)), HarbrrAPIKeyEncrypted: "",
			KeyID: s.keyring.KeyID(), Enabled: r.Enabled, SyncLevel: r.SyncLevel, IndexScope: r.IndexScope,
			FreeleechMode: r.FreeleechMode, SyncProfileID: profileIDs.remap(r.SyncProfileID),
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return fmt.Errorf("backup: insert app connection %q: %w", r.Name, err)
		}
		harbrrEnc, err := s.sealHarbrrKey(newID, r.HarbrrAPIKey)
		if err != nil {
			return err
		}
		if err := repo.SetConnectionSecrets(ctx, q, newID, harbrrEnc, s.keyring.KeyID()); err != nil {
			return fmt.Errorf("backup: set app connection secrets: %w", err)
		}
		if err := loadIndexerSelection(ctx, q, repo, newID, r.SelectedInstanceIDs, instanceIDs); err != nil {
			return err
		}
	}
	return nil
}

// loadIndexerSelection recreates a connection's scope="selected" set, remapping each
// original instance id to the target's new id. An id with no mapping (its instance was
// dropped from the bundle) is skipped rather than faulting. An older bundle carries no
// ids (nil slice), so no selection is restored — the pre-fix behaviour.
func loadIndexerSelection(ctx context.Context, q dbinterface.Execer, repo database.AppConnections, connID int64, oldIDs []int64, instanceIDs idMap) error {
	for _, oldID := range oldIDs {
		newInstID, ok := instanceIDs[oldID]
		if !ok {
			continue
		}
		if err := repo.SetIndexerSelection(ctx, q, connID, newInstID, true); err != nil {
			return fmt.Errorf("backup: restore indexer selection for connection %d: %w", connID, err)
		}
	}
	return nil
}

func (s *Service) loadAnnounceConnections(ctx context.Context, q dbinterface.Execer, rows []AnnounceConnRow, announceConnApps, apiKeyIDs idMap) error {
	repo := database.AnnounceConnections{}
	for _, r := range rows {
		appID := announceConnApps[r.ID]
		newID, err := repo.InsertAnnounceConnection(ctx, q, domain.AnnounceConnection{
			Name: r.Name, Kind: r.Kind, AppID: &appID,
			HarbrrAPIKeyID: zeroIfNil(apiKeyIDs.remap(r.HarbrrAPIKeyID)), HarbrrAPIKeyEncrypted: "",
			KeyID: s.keyring.KeyID(), Enabled: r.Enabled, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return fmt.Errorf("backup: insert announce connection %q: %w", r.Name, err)
		}
		harbrrEnc, err := s.sealHarbrrKey(newID, r.HarbrrAPIKey)
		if err != nil {
			return err
		}
		if err := repo.SetAnnounceConnectionSecrets(ctx, q, newID, harbrrEnc, s.keyring.KeyID()); err != nil {
			return fmt.Errorf("backup: set announce connection secrets: %w", err)
		}
	}
	return nil
}

// sealHarbrrKey re-seals a connection's minted harbrr key under the new connection id
// (the app/tool credential is sealed separately, on the App, by apps.Service.Resolve —
// see resolveConnAppForLoad).
func (s *Service) sealHarbrrKey(connID int64, harbrrKey string) (string, error) {
	encrypted, _, err := connresource.Seal(s.keyring, connID, []connresource.Secret{{Discriminator: discHarbrr, Plaintext: harbrrKey}})
	if err != nil {
		return "", fmt.Errorf("backup: seal harbrr key: %w", err)
	}
	return encrypted[0], nil
}

func (s *Service) loadNotifications(ctx context.Context, q dbinterface.Execer, rows []NotificationRow) error {
	repo := database.Notifications{}
	for _, r := range rows {
		newID, err := repo.InsertNotification(ctx, q, domain.Notification{
			Name: r.Name, Type: r.Type, URLEncrypted: "", KeyID: s.keyring.KeyID(),
			Enabled: r.Enabled, OnHealthFailure: r.OnHealthFailure,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return fmt.Errorf("backup: insert notification %q: %w", r.Name, err)
		}
		if err := s.sealSecret(ctx, q, newID, discURL, r.URL, "notification", repo.SetNotificationSecret); err != nil {
			return err
		}
	}
	return nil
}

func loadAppSettings(ctx context.Context, q dbinterface.Execer, rows []AppSettingRow) error {
	repo := database.AppSettings{}
	for _, r := range rows {
		if err := repo.Set(ctx, q, r.Key, r.Value, r.UpdatedAt); err != nil {
			return fmt.Errorf("backup: restore app setting %q: %w", r.Key, err)
		}
	}
	return nil
}

func loadAdmin(ctx context.Context, q dbinterface.Execer, admin *UserRow) error {
	if admin == nil {
		return nil
	}
	if _, err := (database.Users{}).Create(ctx, q, domain.User{
		Username: admin.Username, PasswordHash: admin.PasswordHash,
		CreatedAt: admin.CreatedAt, UpdatedAt: admin.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("backup: restore admin user: %w", err)
	}
	return nil
}

// countRows counts a table (name is always a code constant, never user input).
func countRows(ctx context.Context, q dbinterface.Execer, table string) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, q.Rebind("SELECT COUNT(*) FROM "+table)).Scan(&n); err != nil {
		return 0, fmt.Errorf("backup: count %s: %w", table, err)
	}
	return n, nil
}

func zeroIfNil(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// nullTime formats an optional timestamp for a nullable TEXT column (nil → SQL NULL).
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
