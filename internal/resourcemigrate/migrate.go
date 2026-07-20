// Package resourcemigrate performs the one-time fold of legacy inline proxy /
// FlareSolverr indexer settings into the global proxy + solver resources
// (0012_proxies_solvers). It cannot be a pure-SQL migration: the inline URLs are
// AES-GCM ciphertext the SQL layer cannot decrypt, and identical endpoints are
// deduplicated into a single shared resource. It runs once at boot, guarded by an
// app_meta flag, and is safe to fail — the engine keeps the inline settings as a
// fallback, so an un-migrated instance still works and the migration retries next
// boot. The per-tracker manual-cookie solver is left inline (it is not global).
package resourcemigrate

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// doneFlag is the app_meta key marking the migration complete (idempotency).
const doneFlag = "inline_proxy_solver_migrated"

type deps struct {
	tx       dbinterface.TxQuerier
	kr       *secrets.Keyring
	clock    func() time.Time
	instRepo database.Instances
	proxRepo database.Proxies
	solvRepo database.Solvers
	// dedup collapses identical endpoints into one shared resource within this run.
	proxyByKey  map[string]int64
	solverByKey map[string]int64
}

// Run folds every instance's inline proxy / FlareSolverr settings into shared
// resources, once. A nil return means done (or already done); an error means the
// transaction rolled back and the inline settings are intact for a retry.
func Run(ctx context.Context, db dbinterface.Querier, kr *secrets.Keyring, clock func() time.Time, log zerolog.Logger) error {
	if done, ok, err := (database.AppMeta{}).Get(ctx, db, doneFlag); err != nil {
		return fmt.Errorf("resourcemigrate: read flag: %w", err)
	} else if ok && done == "1" {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("resourcemigrate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	d := &deps{
		tx: tx, kr: kr, clock: clock,
		proxyByKey: map[string]int64{}, solverByKey: map[string]int64{},
	}
	migrated, err := d.migrateAll(ctx)
	if err != nil {
		return err
	}
	if err := (database.AppMeta{}).Set(ctx, tx, doneFlag, "1"); err != nil {
		return fmt.Errorf("resourcemigrate: set flag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("resourcemigrate: commit: %w", err)
	}
	if migrated > 0 {
		log.Info().Int("instances", migrated).Msg("migrated inline proxy/FlareSolverr settings into global resources")
	}
	return nil
}

// migrateAll folds every instance, returning how many were changed.
func (d *deps) migrateAll(ctx context.Context) (int, error) {
	insts, err := d.instRepo.List(ctx, d.tx)
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: list instances: %w", err)
	}
	changed := 0
	for _, inst := range insts {
		settings, err := d.instRepo.Settings(ctx, d.tx, inst.ID)
		if err != nil {
			return 0, fmt.Errorf("resourcemigrate: settings for %q: %w", inst.Slug, err)
		}
		sm := settingsMap(settings)

		// Only fold a slot the instance hasn't already wired. The migration is
		// re-runnable (a failed run rolls back with the flag unset), and in that
		// window the operator can create a resource and point an un-migrated
		// indexer at it via the API. Re-folding then would create a duplicate and
		// SetRefs would clobber their explicit choice — so a non-nil ref means
		// "already wired, leave it alone" (and don't touch its inline settings).
		proxyID := inst.ProxyID
		if proxyID == nil {
			if proxyID, err = d.foldProxy(ctx, inst, sm); err != nil {
				return 0, err
			}
		}
		solverID := inst.SolverID
		if solverID == nil {
			if solverID, err = d.foldSolver(ctx, inst, sm); err != nil {
				return 0, err
			}
		}
		if proxyID == inst.ProxyID && solverID == inst.SolverID {
			continue // nothing folded (no inline config, or both slots already wired)
		}
		if err := d.instRepo.SetRefs(ctx, d.tx, inst.ID, proxyID, solverID, d.clock()); err != nil {
			return 0, fmt.Errorf("resourcemigrate: set refs for %q: %w", inst.Slug, err)
		}
		changed++
	}
	return changed, nil
}

// foldProxy migrates an instance's inline proxy (proxy_type + encrypted proxy_url)
// into a shared resource, deleting the inline settings. Returns nil when there is
// no inline proxy to migrate.
func (d *deps) foldProxy(ctx context.Context, inst domain.IndexerInstance, sm map[string]domain.IndexerSetting) (*int64, error) {
	typ := sm["proxy_type"].Value
	enc := sm["proxy_url"].ValueEncrypted
	if typ == "" || enc == "" {
		return nil, nil //nolint:nilnil // "no inline proxy" is a valid, non-error outcome.
	}
	rawURL, err := d.kr.Decrypt(inst.ID, "proxy_url", enc)
	if err != nil {
		return nil, fmt.Errorf("resourcemigrate: decrypt proxy_url for %q: %w", inst.Slug, err)
	}
	id, err := d.ensureProxy(ctx, typ, rawURL)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"proxy_type", "proxy_url"} {
		if err := d.instRepo.DeleteSetting(ctx, d.tx, inst.ID, name); err != nil {
			return nil, fmt.Errorf("resourcemigrate: strip %q: %w", name, err)
		}
	}
	return &id, nil
}

// foldSolver migrates an instance's inline FlareSolverr solver into a shared
// resource. The manual-cookie solver is deliberately left inline.
func (d *deps) foldSolver(ctx context.Context, inst domain.IndexerInstance, sm map[string]domain.IndexerSetting) (*int64, error) {
	if sm["solver_type"].Value != domain.SolverTypeFlaresolverr {
		return nil, nil //nolint:nilnil // no FlareSolverr solver (or a manual-cookie one, kept inline).
	}
	enc := sm["flaresolverr_url"].ValueEncrypted
	if enc == "" {
		return nil, nil //nolint:nilnil // solver_type set but no URL: nothing usable to migrate.
	}
	rawURL, err := d.kr.Decrypt(inst.ID, "flaresolverr_url", enc)
	if err != nil {
		return nil, fmt.Errorf("resourcemigrate: decrypt flaresolverr_url for %q: %w", inst.Slug, err)
	}
	maxTimeout, _ := strconv.Atoi(sm["flaresolverr_max_timeout"].Value)
	id, err := d.ensureSolver(ctx, rawURL, maxTimeout)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"solver_type", "flaresolverr_url", "flaresolverr_max_timeout"} {
		if err := d.instRepo.DeleteSetting(ctx, d.tx, inst.ID, name); err != nil {
			return nil, fmt.Errorf("resourcemigrate: strip %q: %w", name, err)
		}
	}
	return &id, nil
}

// ensureProxy returns the id of a shared proxy for (type, url), creating it once.
// The inline URL is parsed directly into structured host/port/username/password
// fields (#294 removed the legacy url_encrypted round trip a boot backfill used to
// do right after Run, on the same boot).
func (d *deps) ensureProxy(ctx context.Context, typ, rawURL string) (int64, error) {
	key := typ + "\x00" + rawURL
	if id, ok := d.proxyByKey[key]; ok {
		return id, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: parse proxy url: %w", err)
	}
	port, _ := strconv.Atoi(u.Port())
	if u.Port() == "" {
		// A port-less inline URL previously worked (net/http defaults the dial
		// port by scheme), so backfill the scheme's conventional default rather
		// than 0 — composeProxyURL always emits an explicit host:port.
		port = defaultProxyPort(typ)
	}
	password, _ := u.User.Password()

	now := d.clock()
	id, err := d.proxRepo.InsertProxy(ctx, d.tx, domain.Proxy{
		Name: resourceName("proxy", rawURL, len(d.proxyByKey)), Type: typ,
		Host: u.Hostname(), Port: port, Username: u.User.Username(),
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: insert proxy: %w", err)
	}
	sealed, err := d.kr.Encrypt(id, domain.ProxySecretPassword, password)
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: encrypt proxy password: %w", err)
	}
	if err := d.proxRepo.SetProxySecret(ctx, d.tx, id, sealed, d.kr.KeyID()); err != nil {
		return 0, fmt.Errorf("resourcemigrate: seal proxy: %w", err)
	}
	d.proxyByKey[key] = id
	return id, nil
}

// defaultProxyPort is the conventional port for a proxy scheme, used when an
// inline URL carried none: http 80, https 443, socks5/socks5h 1080.
func defaultProxyPort(scheme string) int {
	switch scheme {
	case domain.ProxyTypeHTTPS:
		return 443
	case domain.ProxyTypeSOCKS5, domain.ProxyTypeSOCKS5H:
		return 1080
	default: // http
		return 80
	}
}

// ensureSolver returns the id of a shared FlareSolverr solver for (url, maxTimeout).
func (d *deps) ensureSolver(ctx context.Context, rawURL string, maxTimeout int) (int64, error) {
	key := rawURL + "\x00" + strconv.Itoa(maxTimeout)
	if id, ok := d.solverByKey[key]; ok {
		return id, nil
	}
	now := d.clock()
	id, err := d.solvRepo.InsertSolver(ctx, d.tx, domain.Solver{
		Name: resourceName("flaresolverr", rawURL, len(d.solverByKey)), Type: domain.SolverTypeFlaresolverr,
		MaxTimeout: maxTimeout, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: insert solver: %w", err)
	}
	sealed, err := d.kr.Encrypt(id, domain.SolverSecretURL, rawURL)
	if err != nil {
		return 0, fmt.Errorf("resourcemigrate: encrypt solver url: %w", err)
	}
	if err := d.solvRepo.SetSolverSecret(ctx, d.tx, id, sealed, d.kr.KeyID()); err != nil {
		return 0, fmt.Errorf("resourcemigrate: seal solver: %w", err)
	}
	d.solverByKey[key] = id
	return id, nil
}

// resourceName derives a readable name from the URL host (not a secret), falling
// back to a numbered default. The credential-bearing part of the URL is never used.
func resourceName(kind, rawURL string, seq int) string {
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return fmt.Sprintf("migrated-%s-%d", kind, seq+1)
}

// settingsMap indexes settings by name for lookup.
func settingsMap(settings []domain.IndexerSetting) map[string]domain.IndexerSetting {
	m := make(map[string]domain.IndexerSetting, len(settings))
	for _, s := range settings {
		m[s.Name] = s
	}
	return m
}
