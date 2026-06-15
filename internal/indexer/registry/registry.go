// Package registry is harbrr's production indexer-instance registry: it persists
// configured indexers (definition id + settings + encrypted credentials), resolves
// a slug to a ready Cardigann engine, and implements the torznab.Provider the
// Torznab handler expects. It is the core of the Prowlarr-style manager.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// errDisabled marks a resolve that found a disabled instance — an expected
// outcome (the indexer is not served), logged quietly, not as a failure.
var errDisabled = errors.New("registry: instance disabled")

// Registry resolves configured indexer slugs to engines and manages their
// lifecycle. Built engines are cached per slug and invalidated on mutation.
type Registry struct {
	db        *database.DB
	instances database.Instances
	health    database.Health
	loader    *loader.Loader
	keyring   secretsKeyring
	clock     func() time.Time
	timeout   time.Duration
	log       zerolog.Logger
	// doerFactory builds the HTTP client an engine drives, given the per-instance
	// ClientParams so the client can vary per indexer (proxy, rate, timeout). It
	// defaults to a cookie-jar client; tests inject an offline replay Doer.
	doerFactory func(ClientParams) (search.Doer, error)

	mu    sync.Mutex
	cache map[string]*indexerAdapter
}

// secretsKeyring is the subset of *secrets.Keyring the registry uses, declared as
// an interface so it stays small and explicit (encrypt/decrypt + key id).
type secretsKeyring interface {
	Encrypt(instanceID int64, setting, plaintext string) (string, error)
	Decrypt(instanceID int64, setting, blob string) (string, error)
	KeyID() string
}

// Option configures the Registry.
type Option func(*Registry)

// WithClock injects the reference clock (timestamps + engine date parsing).
func WithClock(fn func() time.Time) Option {
	return func(r *Registry) {
		if fn != nil {
			r.clock = fn
		}
	}
}

// WithTimeout sets the per-request HTTP timeout for built engines.
func WithTimeout(d time.Duration) Option { return func(r *Registry) { r.timeout = d } }

// WithLogger sets the logger used for resolve failures (errors are redacted).
func WithLogger(l zerolog.Logger) Option { return func(r *Registry) { r.log = l } }

// ClientParams carries the per-instance inputs the doer factory needs to vary the
// HTTP client per indexer. The Phase-4 seam was nullary (every engine shared one
// client shape); this struct is the widening, so adding fields later (proxy, rate)
// never re-breaks the WithDoerFactory Option.
type ClientParams struct {
	Instance domain.IndexerInstance
	Cfg      map[string]string
	// Timeout is the per-instance request timeout (resolved in build() from a
	// per-instance "timeout" setting, else the registry default); newDoer clamps
	// <=0 to defaultHTTPTimeout.
	Timeout time.Duration
	// RateInterval is the per-host minimum spacing (resolved from the def's
	// requestDelay, else defaultRateInterval).
	RateInterval time.Duration
}

// WithDoerFactory overrides how the HTTP client for a built engine is created
// (tests inject an offline replay Doer; a later phase injects a proxy/paced client).
func WithDoerFactory(fn func(ClientParams) (search.Doer, error)) Option {
	return func(r *Registry) {
		if fn != nil {
			r.doerFactory = fn
		}
	}
}

// New builds a Registry over the given store, definition loader, and keyring.
func New(db *database.DB, ldr *loader.Loader, keyring secretsKeyring, opts ...Option) *Registry {
	r := &Registry{
		db:      db,
		loader:  ldr,
		keyring: keyring,
		clock:   time.Now,
		timeout: defaultHTTPTimeout,
		log:     zerolog.Nop(),
		cache:   map[string]*indexerAdapter{},
	}
	for _, o := range opts {
		o(r)
	}
	if r.doerFactory == nil {
		r.doerFactory = newDoer
	}
	return r
}

// Indexer resolves a slug to its Indexer, implementing torznab.Provider. A
// missing, disabled, or unbuildable instance returns ok=false so the handler
// degrades cleanly (returns the standard "indexer not supported" error).
func (r *Registry) Indexer(ctx context.Context, slug string) (torznab.Indexer, bool) {
	a, err := r.resolve(ctx, slug)
	if err != nil {
		r.logResolveError(slug, err)
		return nil, false
	}
	return a, true
}

// resolve returns the cached adapter for a slug or builds and caches it. Build
// happens outside the lock (it does DB I/O + crypto); a double-check after build
// means that if two goroutines race to build the same uncached slug, the first to
// cache wins and the other reuses it rather than installing a duplicate engine.
func (r *Registry) resolve(ctx context.Context, slug string) (*indexerAdapter, error) {
	r.mu.Lock()
	if a, ok := r.cache[slug]; ok {
		r.mu.Unlock()
		return a, nil
	}
	r.mu.Unlock()

	a, err := r.build(ctx, slug)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[slug]; ok {
		return cached, nil // another goroutine built it first; reuse its engine
	}
	r.cache[slug] = a
	return a, nil
}

// build loads the instance + definition, decrypts its settings into the engine
// config, and constructs the engine + adapter.
func (r *Registry) build(ctx context.Context, slug string) (*indexerAdapter, error) {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return nil, fmt.Errorf("registry: load instance %q: %w", slug, err)
	}
	if !inst.Enabled {
		return nil, errDisabled
	}
	def, err := r.loader.Load(inst.DefinitionID)
	if err != nil {
		return nil, fmt.Errorf("registry: load definition %q: %w", inst.DefinitionID, err)
	}
	settings, err := r.instances.Settings(ctx, r.db, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("registry: load settings for %q: %w", slug, err)
	}
	cfg, err := r.decryptConfig(inst.ID, settings)
	if err != nil {
		return nil, err
	}

	doer, err := r.doerFactory(ClientParams{
		Instance:     inst,
		Cfg:          cfg,
		Timeout:      resolveTimeout(cfg, r.timeout),
		RateInterval: rateInterval(def),
	})
	if err != nil {
		return nil, err
	}
	opts := []cardigann.Option{
		cardigann.WithDoer(doer),
		cardigann.WithConfig(cfg),
		cardigann.WithClock(r.clock),
		// Wire an anti-bot solver from the instance settings ("solver_type" + the
		// encrypted "cookie"); a no-op when unset. FlareSolverr is Phase 6.
		cardigann.SolverOption(cfg),
	}
	if inst.BaseURL != "" {
		opts = append(opts, cardigann.WithBaseURL(inst.BaseURL))
	}
	eng, err := cardigann.NewEngine(def, opts...)
	if err != nil {
		return nil, fmt.Errorf("registry: build engine for %q: %w", slug, err)
	}
	return &indexerAdapter{
		info:       indexerInfo(inst, def),
		engine:     eng,
		instanceID: inst.ID,
		db:         r.db,
		health:     r.health,
		clock:      r.clock,
		log:        r.log,
	}, nil
}

// decryptConfig turns stored settings into the engine's .Config map, decrypting
// each secret with the row-bound AAD.
func (r *Registry) decryptConfig(instanceID int64, settings []domain.IndexerSetting) (map[string]string, error) {
	cfg := make(map[string]string, len(settings))
	for _, s := range settings {
		if !s.IsSecret {
			cfg[s.Name] = s.Value
			continue
		}
		pt, err := r.keyring.Decrypt(instanceID, s.Name, s.ValueEncrypted)
		if err != nil {
			return nil, fmt.Errorf("registry: decrypt setting %q: %w", s.Name, err)
		}
		cfg[s.Name] = pt
	}
	return cfg, nil
}

// logResolveError logs a genuine resolve failure with the error redacted; a
// not-found or disabled instance is expected and stays quiet.
func (r *Registry) logResolveError(slug string, err error) {
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, errDisabled) {
		return
	}
	r.log.Error().
		Str("indexer", slug).
		Str("error", apphttp.RedactURL(err.Error())).
		Msg("registry: resolve failed")
}

// invalidate drops a slug's cached engine so the next resolve rebuilds it.
func (r *Registry) invalidate(slug string) {
	r.mu.Lock()
	delete(r.cache, slug)
	r.mu.Unlock()
}

// inTx runs fn inside a transaction, committing on success and rolling back on
// error. The repo methods fn calls take an Execer, which the TxQuerier satisfies,
// so an instance and its settings are written atomically.
func (r *Registry) inTx(ctx context.Context, fn func(tx dbinterface.TxQuerier) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("registry: commit: %w", err)
	}
	return nil
}

// indexerInfo assembles the public indexer identity from the instance + def (no
// secrets).
func indexerInfo(inst domain.IndexerInstance, def *loader.Definition) torznab.IndexerInfo {
	site := inst.BaseURL
	if site == "" && len(def.Links) > 0 {
		site = def.Links[0]
	}
	return torznab.IndexerInfo{
		ID:          inst.Slug,
		Name:        orDefault(inst.Name, def.Name),
		Description: def.Description,
		SiteLink:    site,
		Type:        def.Type,
	}
}

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
