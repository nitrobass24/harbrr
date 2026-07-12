package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Secret AAD discriminators, byte-matching the sealing services (the single source of
// truth is database.SecretSurfaces()). proxies/solvers use the exported domain consts.
const (
	discApp    = "app"    // *_connections api_key_encrypted
	discHarbrr = "harbrr" // *_connections harbrr_api_key_encrypted
	discURL    = "url"    // notifications url_encrypted
)

// collect reads every backed-up table and decrypts each secret with the current keyring,
// producing the cleartext-secrets payload the caller then seals under the passphrase.
func (s *Service) collect(ctx context.Context, q dbinterface.Execer) (*Tables, error) {
	var (
		t   Tables
		err error
	)
	if t.Proxies, err = s.collectProxies(ctx, q); err != nil {
		return nil, err
	}
	if t.Solvers, err = s.collectSolvers(ctx, q); err != nil {
		return nil, err
	}
	if t.SyncProfiles, err = collectSyncProfiles(ctx, q); err != nil {
		return nil, err
	}
	if t.APIKeys, err = collectAPIKeys(ctx, q); err != nil {
		return nil, err
	}
	if t.IndexerInstances, err = s.collectInstances(ctx, q); err != nil {
		return nil, err
	}
	if t.AppConnections, err = s.collectAppConnections(ctx, q); err != nil {
		return nil, err
	}
	if t.AnnounceConnections, err = s.collectAnnounceConnections(ctx, q); err != nil {
		return nil, err
	}
	if t.Notifications, err = s.collectNotifications(ctx, q); err != nil {
		return nil, err
	}
	if t.AppSettings, err = collectAppSettings(ctx, q); err != nil {
		return nil, err
	}
	admin, found, err := collectAdmin(ctx, q)
	if err != nil {
		return nil, err
	}
	if found {
		t.Admin = &admin
	}
	return &t, nil
}

// decryptSecret decrypts a stored ciphertext for (id, discriminator). An empty column is
// an absent secret (returned as ""), never fed to Decrypt. The keyring returns the value
// verbatim in plaintext mode. The error is leak-free (keyring.Decrypt never echoes data).
func (s *Service) decryptSecret(id int64, disc, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	pt, err := s.keyring.Decrypt(id, disc, ciphertext)
	if err != nil {
		return "", fmt.Errorf("backup: decrypt %s secret for id %d: %w", disc, id, err)
	}
	return pt, nil
}

func (s *Service) collectProxies(ctx context.Context, q dbinterface.Execer) ([]ProxyRow, error) {
	list, err := (database.Proxies{}).ListProxies(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list proxies: %w", err)
	}
	out := make([]ProxyRow, 0, len(list))
	for _, p := range list {
		url, err := s.decryptSecret(p.ID, domain.ProxySecretURL, p.URLEncrypted)
		if err != nil {
			return nil, err
		}
		out = append(out, ProxyRow{
			ID: p.ID, Name: p.Name, Type: p.Type, URL: url,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) collectSolvers(ctx context.Context, q dbinterface.Execer) ([]SolverRow, error) {
	list, err := (database.Solvers{}).ListSolvers(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list solvers: %w", err)
	}
	out := make([]SolverRow, 0, len(list))
	for _, sv := range list {
		url, err := s.decryptSecret(sv.ID, domain.SolverSecretURL, sv.URLEncrypted)
		if err != nil {
			return nil, err
		}
		out = append(out, SolverRow{
			ID: sv.ID, Name: sv.Name, Type: sv.Type, URL: url, MaxTimeout: sv.MaxTimeout,
			CreatedAt: sv.CreatedAt, UpdatedAt: sv.UpdatedAt,
		})
	}
	return out, nil
}

func collectSyncProfiles(ctx context.Context, q dbinterface.Execer) ([]SyncProfileRow, error) {
	list, err := (database.SyncProfiles{}).ListProfiles(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list sync profiles: %w", err)
	}
	out := make([]SyncProfileRow, 0, len(list))
	for _, p := range list {
		out = append(out, SyncProfileRow{
			ID: p.ID, Name: p.Name, Categories: p.Categories, MinSeeders: p.MinSeeders,
			EnableRss: p.EnableRss, EnableAutomaticSearch: p.EnableAutomaticSearch,
			EnableInteractiveSearch: p.EnableInteractiveSearch,
			CreatedAt:               p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, nil
}

func collectAPIKeys(ctx context.Context, q dbinterface.Execer) ([]APIKeyRow, error) {
	list, err := (database.APIKeys{}).List(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list api keys: %w", err)
	}
	out := make([]APIKeyRow, 0, len(list))
	for _, k := range list {
		out = append(out, APIKeyRow{
			ID: k.ID, Name: k.Name, KeyHash: k.KeyHash, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		})
	}
	return out, nil
}

func (s *Service) collectInstances(ctx context.Context, q dbinterface.Execer) ([]InstanceRow, error) {
	list, err := (database.Instances{}).List(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list indexers: %w", err)
	}
	out := make([]InstanceRow, 0, len(list))
	for _, inst := range list {
		settings, err := s.collectSettings(ctx, q, inst.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, InstanceRow{
			ID: inst.ID, Slug: inst.Slug, DefinitionID: inst.DefinitionID, Name: inst.Name,
			BaseURL: inst.BaseURL, Enabled: inst.Enabled, Protocol: inst.Protocol,
			ProxyID: inst.ProxyID, SolverID: inst.SolverID,
			CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt, Settings: settings,
		})
	}
	return out, nil
}

func (s *Service) collectSettings(ctx context.Context, q dbinterface.Execer, instanceID int64) ([]SettingRow, error) {
	settings, err := (database.Instances{}).Settings(ctx, q, instanceID)
	if err != nil {
		return nil, fmt.Errorf("backup: list settings for indexer %d: %w", instanceID, err)
	}
	out := make([]SettingRow, 0, len(settings))
	for _, st := range settings {
		value := st.Value
		if st.IsSecret {
			if value, err = s.decryptSecret(instanceID, st.Name, st.ValueEncrypted); err != nil {
				return nil, err
			}
		}
		out = append(out, SettingRow{Name: st.Name, Value: value, IsSecret: st.IsSecret})
	}
	return out, nil
}

func (s *Service) collectAppConnections(ctx context.Context, q dbinterface.Execer) ([]AppConnRow, error) {
	list, err := (database.AppConnections{}).ListConnections(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list app connections: %w", err)
	}
	out := make([]AppConnRow, 0, len(list))
	for _, c := range list {
		appKey, err := s.decryptSecret(c.ID, discApp, c.APIKeyEncrypted)
		if err != nil {
			return nil, err
		}
		harbrrKey, err := s.decryptSecret(c.ID, discHarbrr, c.HarbrrAPIKeyEncrypted)
		if err != nil {
			return nil, err
		}
		out = append(out, AppConnRow{
			ID: c.ID, Name: c.Name, Kind: c.Kind, BaseURL: c.BaseURL, APIKey: appKey,
			HarbrrURL: c.HarbrrURL, HarbrrAPIKeyID: nilIfZero(c.HarbrrAPIKeyID), HarbrrAPIKey: harbrrKey,
			Enabled: c.Enabled, SyncLevel: c.SyncLevel, IndexScope: c.IndexScope,
			FreeleechMode: c.FreeleechMode, Priority: c.Priority, SyncProfileID: c.SyncProfileID,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) collectAnnounceConnections(ctx context.Context, q dbinterface.Execer) ([]AnnounceConnRow, error) {
	list, err := (database.AnnounceConnections{}).ListAnnounceConnections(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list announce connections: %w", err)
	}
	out := make([]AnnounceConnRow, 0, len(list))
	for _, c := range list {
		appKey, err := s.decryptSecret(c.ID, discApp, c.APIKeyEncrypted)
		if err != nil {
			return nil, err
		}
		harbrrKey, err := s.decryptSecret(c.ID, discHarbrr, c.HarbrrAPIKeyEncrypted)
		if err != nil {
			return nil, err
		}
		out = append(out, AnnounceConnRow{
			ID: c.ID, Name: c.Name, Kind: c.Kind, BaseURL: c.BaseURL, APIKey: appKey,
			HarbrrURL: c.HarbrrURL, HarbrrAPIKeyID: nilIfZero(c.HarbrrAPIKeyID), HarbrrAPIKey: harbrrKey,
			Enabled: c.Enabled, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) collectNotifications(ctx context.Context, q dbinterface.Execer) ([]NotificationRow, error) {
	list, err := (database.Notifications{}).ListNotifications(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup: list notifications: %w", err)
	}
	out := make([]NotificationRow, 0, len(list))
	for _, n := range list {
		url, err := s.decryptSecret(n.ID, discURL, n.URLEncrypted)
		if err != nil {
			return nil, err
		}
		out = append(out, NotificationRow{
			ID: n.ID, Name: n.Name, Type: n.Type, URL: url, Enabled: n.Enabled,
			OnHealthFailure: n.OnHealthFailure, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		})
	}
	return out, nil
}

// collectAppSettings reads the runtime config kv table via raw SQL so updated_at survives
// the round-trip (the repo's GetAll drops it). app_settings never holds a secret.
func collectAppSettings(ctx context.Context, q dbinterface.Execer) ([]AppSettingRow, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT key, value, updated_at FROM app_settings ORDER BY key`))
	if err != nil {
		return nil, fmt.Errorf("backup: query app settings: %w", err)
	}
	defer rows.Close()

	var out []AppSettingRow
	for rows.Next() {
		var key, value, updatedAt string
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return nil, fmt.Errorf("backup: scan app setting: %w", err)
		}
		out = append(out, AppSettingRow{Key: key, Value: value, UpdatedAt: parseRFC3339(updatedAt)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("backup: iterate app settings: %w", err)
	}
	return out, nil
}

// collectAdmin captures the single admin user (its one-way password hash rides along so a
// migrate keeps the login). found is false for a fresh instance with no user yet.
func collectAdmin(ctx context.Context, q dbinterface.Execer) (row UserRow, found bool, err error) {
	u, err := (database.Users{}).GetAdmin(ctx, q)
	if errors.Is(err, database.ErrNotFound) {
		return UserRow{}, false, nil
	}
	if err != nil {
		return UserRow{}, false, fmt.Errorf("backup: get admin user: %w", err)
	}
	return UserRow{
		ID: u.ID, Username: u.Username, PasswordHash: u.PasswordHash,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}, true, nil
}

// nilIfZero maps a 0 FK id (harbrr's "no reference" sentinel) to a nil pointer.
func nilIfZero(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// parseRFC3339 parses a stored RFC3339 UTC timestamp, returning the zero time on a
// malformed value (defensive, mirroring the database package's parseTime).
func parseRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
