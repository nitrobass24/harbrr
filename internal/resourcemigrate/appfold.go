package resourcemigrate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// appsFoldedFlag marks the App fold complete (idempotency), like doneFlag.
const appsFoldedFlag = "apps_folded"

// FoldApps folds every legacy surface row (app-sync / announce / download) into a
// first-class App (ADR 0004 §7): decrypt the row's credential under its own id, get-
// or-create the App by (kind, base_url), re-seal the credential under the App's id,
// and set the row's app_id. It runs once at boot, guarded by an app_meta flag, in one
// transaction — non-fatal and safe to fail (a rollback leaves every legacy column
// intact and app_id NULL, so surfaces surface ErrAppMigrationPending until the next
// boot retries). Duplicate identities across surfaces reconcile newest-credential-wins
// (processed newest-first); an older row whose credential differs is logged loudly,
// redacted, never the value. A host-less download client (blackhole) has no identity
// to fold and is skipped.
func FoldApps(ctx context.Context, db dbinterface.Querier, kr *secrets.Keyring, clock func() time.Time, log zerolog.Logger) error {
	if done, ok, err := (database.AppMeta{}).Get(ctx, db, appsFoldedFlag); err != nil {
		return fmt.Errorf("resourcemigrate: read apps flag: %w", err)
	} else if ok && done == "1" {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("resourcemigrate: begin apps tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	f := &appFold{tx: tx, kr: kr, clock: clock, log: log, appByKey: map[string]int64{}, credByKey: map[string]string{}}
	folded, err := f.run(ctx)
	if err != nil {
		return err
	}
	if err := (database.AppMeta{}).Set(ctx, tx, appsFoldedFlag, "1"); err != nil {
		return fmt.Errorf("resourcemigrate: set apps flag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("resourcemigrate: commit apps fold: %w", err)
	}
	if folded > 0 {
		log.Info().Int("rows", folded).Msg("folded legacy surface rows into first-class apps")
	}
	return nil
}

// appFold carries the fold's transaction, keyring, and within-run dedup state.
type appFold struct {
	tx    dbinterface.TxQuerier
	kr    *secrets.Keyring
	clock func() time.Time
	log   zerolog.Logger
	// appByKey / credByKey collapse identical (kind, base_url) identities across
	// surfaces into one App within this run, tracking the winning (newest) credential
	// so a later, older row with a different credential can be flagged.
	appByKey  map[string]int64
	credByKey map[string]string
}

// foldRow is one legacy surface row awaiting fold, normalized across the three tables.
type foldRow struct {
	surface   string
	rowID     int64
	kind      string
	name      string
	baseURL   string
	username  string
	harbrrURL string
	encrypted string
	discrim   string
	updatedAt time.Time
}

// run gathers every un-folded candidate row, processes them newest-first, and returns
// how many were folded.
func (f *appFold) run(ctx context.Context) (int, error) {
	rows, err := f.gather(ctx)
	if err != nil {
		return 0, err
	}
	// Newest-credential-wins: the first row processed for a (kind, base_url) creates
	// the App with its credential; older rows reuse it (and flag a mismatch).
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].updatedAt.After(rows[j].updatedAt) })

	folded := 0
	for _, r := range rows {
		if err := f.foldOne(ctx, r); err != nil {
			return 0, err
		}
		folded++
	}
	return folded, nil
}

// gather collects the un-folded rows of all three surfaces into one normalized slice.
func (f *appFold) gather(ctx context.Context) ([]foldRow, error) {
	var out []foldRow
	appConns, err := (database.AppConnections{}).ListConnections(ctx, f.tx)
	if err != nil {
		return nil, fmt.Errorf("resourcemigrate: list app connections: %w", err)
	}
	for _, c := range appConns {
		if c.AppID != nil {
			continue
		}
		out = append(out, foldRow{
			surface: "app-sync", rowID: c.ID, kind: c.Kind, name: c.Name, baseURL: c.BaseURL,
			harbrrURL: c.HarbrrURL, encrypted: c.APIKeyEncrypted, discrim: domain.AppSecret, updatedAt: c.UpdatedAt,
		})
	}
	annConns, err := (database.AnnounceConnections{}).ListAnnounceConnections(ctx, f.tx)
	if err != nil {
		return nil, fmt.Errorf("resourcemigrate: list announce connections: %w", err)
	}
	for _, c := range annConns {
		if c.AppID != nil {
			continue
		}
		out = append(out, foldRow{
			surface: "announce", rowID: c.ID, kind: c.Kind, name: c.Name, baseURL: c.BaseURL,
			harbrrURL: c.HarbrrURL, encrypted: c.APIKeyEncrypted, discrim: domain.AppSecret, updatedAt: c.UpdatedAt,
		})
	}
	dls, err := (database.DownloadClients{}).ListDownloadClients(ctx, f.tx)
	if err != nil {
		return nil, fmt.Errorf("resourcemigrate: list download clients: %w", err)
	}
	for _, c := range dls {
		// A host-less client (blackhole) has no network identity or credential — no App.
		if c.AppID != nil || c.Host == "" {
			continue
		}
		out = append(out, foldRow{
			surface: "download", rowID: c.ID, kind: c.Kind, name: c.Name, baseURL: c.Host, username: c.Username,
			encrypted: c.SecretEncrypted, discrim: domain.DownloadClientSecret, updatedAt: c.UpdatedAt,
		})
	}
	return out, nil
}

// foldOne decrypts a row's credential, get-or-creates its App, and links the row.
func (f *appFold) foldOne(ctx context.Context, r foldRow) error {
	cred := ""
	if r.encrypted != "" {
		plain, err := f.kr.Decrypt(r.rowID, r.discrim, r.encrypted)
		if err != nil {
			return fmt.Errorf("resourcemigrate: decrypt %s row %d: %w", r.surface, r.rowID, err)
		}
		cred = plain
	}
	appID, err := f.ensureApp(ctx, r, cred)
	if err != nil {
		return err
	}
	return f.linkRow(ctx, r, appID)
}

// ensureApp returns the App id for a row's (kind, base_url), creating it once and
// sealing the (newest) credential under the App's own id. A later, older row with the
// same identity reuses the App; a differing credential is logged loudly (never the
// value).
func (f *appFold) ensureApp(ctx context.Context, r foldRow, cred string) (int64, error) {
	key := r.kind + "\x00" + r.baseURL
	if id, ok := f.appByKey[key]; ok {
		if cred != f.credByKey[key] {
			f.log.Warn().Str("kind", r.kind).Str("surface", r.surface).Int64("row", r.rowID).
				Msg("app-fold: same app configured with a different credential across surfaces; keeping the newest, ignoring the older (credential not logged)")
		}
		return id, nil
	}
	// Adopt an App an operator may have created via the API during a prior failed
	// run's retry window; else create it.
	if existing, err := (database.Apps{}).GetAppByIdentity(ctx, f.tx, r.kind, r.baseURL); err == nil {
		f.appByKey[key], f.credByKey[key] = existing.ID, cred
		return existing.ID, nil
	} else if !errors.Is(err, database.ErrNotFound) {
		return 0, fmt.Errorf("resourcemigrate: lookup app for %s row %d: %w", r.surface, r.rowID, err)
	}
	return f.createApp(ctx, r, cred, key)
}

// createApp inserts the App and seals its credential under the App's own id.
func (f *appFold) createApp(ctx context.Context, r foldRow, cred, key string) (int64, error) {
	name := r.name
	if name == "" {
		name = r.kind
	}
	now := f.clock()
	repo := database.Apps{}
	id, err := repo.InsertApp(ctx, f.tx, domain.App{
		Kind: r.kind, Name: name, BaseURL: r.baseURL, Username: r.username,
		HarbrrURL: r.harbrrURL, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: insert app for %s row %d: %w", r.surface, r.rowID, err)
	}
	sealed, err := f.kr.Encrypt(id, domain.AppSecret, cred)
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: seal app credential: %w", err)
	}
	if err := repo.SetAppSecret(ctx, f.tx, id, sealed, f.kr.KeyID()); err != nil {
		return 0, fmt.Errorf("resourcemigrate: set app secret: %w", err)
	}
	f.appByKey[key], f.credByKey[key] = id, cred
	return id, nil
}

// linkRow writes app_id back onto the row's surface table.
func (f *appFold) linkRow(ctx context.Context, r foldRow, appID int64) error {
	var err error
	switch r.surface {
	case "announce":
		err = (database.AnnounceConnections{}).SetAnnounceConnectionAppID(ctx, f.tx, r.rowID, appID)
	case "download":
		err = (database.DownloadClients{}).SetDownloadClientAppID(ctx, f.tx, r.rowID, appID)
	default:
		err = (database.AppConnections{}).SetConnectionAppID(ctx, f.tx, r.rowID, appID)
	}
	if err != nil {
		return fmt.Errorf("resourcemigrate: link %s row %d: %w", r.surface, r.rowID, err)
	}
	return nil
}
