// Package registry is harbrr's production indexer-instance registry: it persists
// configured indexers (definition id + settings + encrypted credentials), resolves
// a slug to a ready indexer (a Cardigann engine or a native family driver), and
// implements the torznabhttp.Provider the Torznab handler expects. It is the core of the
// Prowlarr-style manager.
package registry

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
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
	"github.com/autobrr/harbrr/internal/indexer/native"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// errDisabled marks a resolve that found a disabled instance — an expected
// outcome (the indexer is not served), logged quietly, not as a failure.
var errDisabled = errors.New("registry: instance disabled")

// Resolver resolves configured indexer slugs to engines and is the invalidation
// authority: built engines are cached per slug (guarded by mu) and invalidated on
// mutation. It is the serve/resolve half of the registry — the latency-sensitive,
// lock-heavy hot path — separated from transactional CRUD (Manager) and health/stats
// reporting (StatsReporter).
type Resolver struct {
	db        dbinterface.Querier
	instances database.Instances
	proxies   database.Proxies
	solvers   database.Solvers
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

	// native maps a native family's definition id to its (Go-built def + factory).
	// An instance whose DefinitionID is here builds the native driver instead of a
	// Cardigann engine; everything else (caching, health, /dl, serializer) is shared.
	native map[string]native.Family

	// searchCache, when non-nil, is wired into each resolved adapter so its Search runs
	// cache-aside (the served path only — Test stays uncached). Nil means caching is OFF
	// and the adapter runs live.
	searchCache *SearchCache

	// healthSink, when non-nil, is notified best-effort after a health event is
	// recorded (e.g. the notify service fans it out to configured targets). Nil (the
	// default) means no notification — recording is unchanged.
	healthSink HealthSink

	// stats holds the durable per-indexer query/grab/latency counters. Always present
	// (built in New), instrumented by the per-instance indexerAdapter, rehydrated at
	// boot and flushed periodically + at shutdown. Failure counts are folded in at read
	// time from the health events, not tracked here.
	stats *IndexerStats

	mu sync.Mutex
	// cache holds the per-slug served indexer — the flattened adapter (cache-wired when
	// searchCache != nil, else running live), served as a torznabhttp.Indexer.
	cache map[string]torznabhttp.Indexer
	// gen is a per-slug generation counter bumped by invalidate; epoch is a global
	// counter bumped by InvalidateAll. resolve captures both before it builds an
	// engine outside the lock, and refuses to install that engine if either moved
	// during the build. Without this, an invalidate landing mid-build is a no-op (the
	// slug isn't cached yet) and resolve installs an engine built from pre-invalidation
	// settings — a persistently stale engine until the next mutation (U8R-F3).
	gen   map[string]uint64
	epoch uint64
}

// Registry is the composed facade the whole application holds. It embeds the three focused
// types so New's signature and every external call site (Add / Indexer / Stats / …) stay
// unchanged, while each concern owns one concurrency story. Method promotion exposes all
// three surfaces off one value; the method names don't collide across the three.
type Registry struct {
	*Resolver
	*Manager
	*StatsReporter
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

// WithClock injects the reference clock (timestamps + engine date parsing). It qualifies
// the field explicitly (r.Resolver.clock): clock, db, and instances are each duplicated
// across all three embedded types (Resolver/Manager/StatsReporter), so those promoted
// selectors are ambiguous on the facade — an option touching any of them must name the
// intended embedded type. New copies the finalized clock into the Manager/StatsReporter
// after options run.
func WithClock(fn func() time.Time) Option {
	return func(r *Registry) {
		if fn != nil {
			r.Resolver.clock = fn
		}
	}
}

// WithLogger sets the logger used for resolve failures (errors are redacted).
func WithLogger(l zerolog.Logger) Option { return func(r *Registry) { r.log = l } }

// WithSearchCache enables the search-results cache: each resolved adapter is wired to it
// and its Search runs cache-aside. Nil (the default, when this Option is not passed)
// leaves caching off with zero behavior change.
func WithSearchCache(sc *SearchCache) Option {
	return func(r *Registry) { r.searchCache = sc }
}

// HealthSink receives a best-effort call after a classified health event is recorded,
// with the indexer slug, event kind, and credential-scrubbed detail. Implementations
// (the notify service) must not block or error back into the search path — they own
// their own async dispatch. Declared here (structurally satisfied) so the registry
// never imports the notification package.
type HealthSink interface {
	OnHealthEvent(ctx context.Context, indexer, kind, detail string)
}

// WithHealthSink registers the sink notified after each recorded health event. Nil (the
// default) leaves health recording unchanged with no notification.
func WithHealthSink(sink HealthSink) Option {
	return func(r *Registry) { r.healthSink = sink }
}

// ClientParams carries the per-instance inputs the doer factory needs to vary the
// HTTP client per indexer. The original seam was nullary (every engine shared one
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
	// Logger is the registry logger threaded into the paced doer so outbound requests
	// trace (method/redacted-URL/status/duration) at debug. A zero value is fine: the
	// registry defaults r.log to zerolog.Nop(), on which Debug()/Trace() are no-ops.
	Logger zerolog.Logger
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

// New builds a Registry facade over the given store, definition loader, keyring, and
// native-family catalog. families is the caller's native driver catalog (production
// wiring passes catalog.All(); a test that only exercises the Cardigann engine path
// may pass an explicitly-nil map — a deliberate, visible choice rather than a hidden
// default). It is a required parameter, not an Option: a default-empty catalog would
// make every native indexer silently fail to resolve. It constructs the Resolver
// first (options mutate it via r.Resolver.*), applies the doerFactory/stats defaults,
// then builds the Manager and StatsReporter from the Resolver's now-finalized
// handles — so each focused type carries only the state its methods use, all sharing
// the same store handles + the single *IndexerStats.
func New(db dbinterface.Querier, ldr *loader.Loader, keyring secretsKeyring, families map[string]native.Family, opts ...Option) *Registry {
	res := &Resolver{
		db:      db,
		loader:  ldr,
		keyring: keyring,
		clock:   time.Now,
		timeout: defaultHTTPTimeout,
		log:     zerolog.Nop(),
		native:  families,
		cache:   map[string]torznabhttp.Indexer{},
		gen:     map[string]uint64{},
	}
	r := &Registry{Resolver: res}
	for _, o := range opts {
		o(r)
	}
	if res.doerFactory == nil {
		res.doerFactory = newDoer
	}
	// Built after the options loop so it captures the final clock/log, exactly like the
	// doerFactory default above.
	if res.stats == nil {
		res.stats = newIndexerStats(db, res.clock, res.log)
	}
	// Manager and StatsReporter are built last, from the resolver's finalized handles: the
	// same clock and the same *IndexerStats pointer. Manager evicts the serve path through
	// the resolver via the invalidator seam (inv: res); it never holds a *Resolver.
	r.Manager = &Manager{
		db:        res.db,
		instances: res.instances,
		keyring:   res.keyring,
		clock:     res.clock,
		loader:    res.loader,
		native:    res.native,
		inv:       res,
	}
	r.StatsReporter = &StatsReporter{
		stats:     res.stats,
		instances: res.instances,
		health:    res.health,
		db:        res.db,
		clock:     res.clock,
	}
	return r
}

// Indexer resolves a slug to its Indexer, implementing torznabhttp.Provider. A
// missing, disabled, or unbuildable instance returns ok=false so the handler
// degrades cleanly (returns the standard "indexer not supported" error).
func (r *Resolver) Indexer(ctx context.Context, slug string) (torznabhttp.Indexer, bool) {
	idx, err := r.resolve(ctx, slug)
	if err != nil {
		r.logResolveError(slug, err)
		return nil, false
	}
	return idx, true
}

// resolve returns the cached adapter for a slug or builds and caches it. Build
// happens outside the lock (it does DB I/O + crypto); a double-check after build
// means that if two goroutines race to build the same uncached slug, the first to
// cache wins and the other reuses it rather than installing a duplicate engine.
//
// build reads the instance's settings (proxy/solver refs, credentials) at build
// time, so an invalidate landing during the build makes the just-built engine
// stale. Because the slug is not cached while building, that invalidate's
// delete(cache) is a no-op and cannot stop the install on its own. resolve
// therefore captures the slug's generation and the global epoch before building and
// declines to cache an engine whose generation moved: the stale engine is served
// for this one request but never installed, so the next resolve rebuilds fresh
// (U8R-F3).
func (r *Resolver) resolve(ctx context.Context, slug string) (torznabhttp.Indexer, error) {
	r.mu.Lock()
	if idx, ok := r.cache[slug]; ok {
		r.mu.Unlock()
		return idx, nil
	}
	genSlug, epoch := r.gen[slug], r.epoch
	r.mu.Unlock()

	idx, err := r.build(ctx, slug)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[slug]; ok {
		return cached, nil // another goroutine built it first; reuse its engine
	}
	if r.gen[slug] != genSlug || r.epoch != epoch {
		// An invalidate (this slug) or InvalidateAll landed during build: idx was
		// built from now-superseded settings. Return it for this request only —
		// leaving the cache empty so the next resolve rebuilds fresh — rather than
		// installing a stale engine that would persist until the next mutation.
		return idx, nil
	}
	r.cache[slug] = idx
	return idx, nil
}

// build resolves the served indexer for a slug: the flattened adapter, wired to the
// search cache when caching is configured. The adapter owns the cache-aside read and the
// freeleech serve-time view as an inline top-to-bottom sequence (see indexerAdapter.
// Search) — no decorator stack. resolve caches and serves this; Test deliberately uses
// buildAdapter, which leaves cache nil so a credential probe never warms the cache.
func (r *Resolver) build(ctx context.Context, slug string) (torznabhttp.Indexer, error) {
	a, err := r.buildAdapter(ctx, slug)
	if err != nil {
		return nil, err
	}
	if r.searchCache != nil {
		a.cache = r.searchCache
		// Snapshot the instance's invalidation generation now, at build time — the same
		// capture wrap() used to take. storeBestEffort drops any write-back from a
		// superseded generation, and capturing here (not per fetch) also catches a purge
		// that lands between this resolve and a later SWR trigger (U8R-F4).
		a.builtEpoch = r.searchCache.instanceEpoch(a.instanceID)
	}
	return a, nil
}

// buildAdapter loads the instance + definition, decrypts its settings, and
// constructs the engine-shaped core (Cardigann engine OR native family driver)
// wrapped in the shared adapter. It returns the adapter with cache left nil — Test
// uses it so a credential probe never consults or warms the cache.
func (r *Resolver) buildAdapter(ctx context.Context, slug string) (*indexerAdapter, error) {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return nil, fmt.Errorf("registry: load instance %q: %w", slug, err)
	}
	if !inst.Enabled {
		return nil, errDisabled
	}
	def, factory, err := resolveDefinition(r.native, r.loader, inst.DefinitionID)
	if err != nil {
		return nil, err
	}
	settings, err := r.instances.Settings(ctx, r.db, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("registry: load settings for %q: %w", slug, err)
	}
	cfg, err := r.decryptConfig(inst.ID, settings)
	if err != nil {
		return nil, err
	}
	// Resolve the instance's referenced global proxy / solver into cfg BEFORE the
	// transport and solver are built, so buildTransport / SolverOption stay
	// unchanged (they only read cfg keys). A reference wins over an inline setting;
	// no reference leaves the inline value (the fallback) in place.
	if err := r.resolveResourceRefs(ctx, inst, cfg); err != nil {
		return nil, err
	}

	// Canonicalize checkbox settings ("True"/"") before anything reads them: a value
	// persisted as the literal "false" is non-empty and would otherwise read as CHECKED
	// under both template truthiness and the freeleech `!= ""` view below (autobrr/harbrr#119).
	cardigann.CanonicalizeCheckboxes(def, cfg)

	// freeleech is consumed as a SERVE-TIME view, not a fetch-time filter: the engine is
	// built with the key cleared so every fetch returns the full catalog (cached once and
	// shared by the honor + bypass feeds). The stored value drives the serve-time freeleech
	// view in indexerAdapter.Search. Go-template truthiness is "non-empty" (a checked box
	// resolves to "True", config.go), so an empty/absent value is off.
	freeleechOnly := cfg["freeleech"] != ""
	engineCfg := cfg
	if freeleechOnly {
		engineCfg = maps.Clone(cfg)
		delete(engineCfg, "freeleech")
	}

	doer, err := r.doerFactory(ClientParams{
		Instance:     inst,
		Cfg:          cfg,
		Timeout:      resolveTimeout(cfg, r.timeout),
		RateInterval: rateInterval(def), // a native def carries RequestDelay, so it is paced too
		Logger:       r.log,
	})
	if err != nil {
		return nil, err
	}
	inner, err := r.buildInner(inst, def, factory, engineCfg, doer)
	if err != nil {
		return nil, err
	}
	return &indexerAdapter{
		info:          indexerInfo(inst, def),
		inner:         inner,
		instanceID:    inst.ID,
		cfg:           cfg,
		freeleechOnly: freeleechOnly,
		db:            r.db,
		health:        r.health,
		healthSink:    r.healthSink,
		stats:         r.stats,
		clock:         r.clock,
		log:           r.log,
	}, nil
}

// resolveResourceRefs merges an instance's referenced global proxy / solver into
// cfg, writing the same keys the inline settings use (proxy_type/proxy_url;
// solver_type/flaresolverr_url/flaresolverr_max_timeout) so buildTransport and
// SolverOption need no change. A reference overrides an inline value; no reference
// leaves the inline fallback in place. A dangling reference (the resource was
// deleted mid-flight, before the ON DELETE SET NULL fired) is skipped, not fatal —
// the indexer degrades to no proxy / no solver.
func (r *Resolver) resolveResourceRefs(ctx context.Context, inst domain.IndexerInstance, cfg map[string]string) error {
	if inst.ProxyID != nil {
		p, err := r.proxies.GetProxy(ctx, r.db, *inst.ProxyID)
		switch {
		case err == nil:
			url, derr := r.keyring.Decrypt(p.ID, domain.ProxySecretURL, p.URLEncrypted)
			if derr != nil {
				return fmt.Errorf("registry: decrypt proxy %d url: %w", p.ID, derr)
			}
			cfg["proxy_type"], cfg["proxy_url"] = p.Type, url
		case !errors.Is(err, database.ErrNotFound):
			return fmt.Errorf("registry: load proxy %d: %w", *inst.ProxyID, err)
		}
	}
	if inst.SolverID != nil {
		s, err := r.solvers.GetSolver(ctx, r.db, *inst.SolverID)
		switch {
		case err == nil:
			url, derr := r.keyring.Decrypt(s.ID, domain.SolverSecretURL, s.URLEncrypted)
			if derr != nil {
				return fmt.Errorf("registry: decrypt solver %d url: %w", s.ID, derr)
			}
			cfg["solver_type"], cfg["flaresolverr_url"] = s.Type, url
			if s.MaxTimeout > 0 {
				cfg["flaresolverr_max_timeout"] = strconv.Itoa(s.MaxTimeout)
			}
		case !errors.Is(err, database.ErrNotFound):
			return fmt.Errorf("registry: load solver %d: %w", *inst.SolverID, err)
		}
	}
	return nil
}

// buildInner constructs the engine-shaped core: a native family driver when a
// factory is present, otherwise the Cardigann engine. Both satisfy native.Driver.
func (r *Resolver) buildInner(inst domain.IndexerInstance, def *loader.Definition, factory native.Factory, cfg map[string]string, doer search.Doer) (native.Driver, error) {
	if factory != nil {
		d, err := factory(native.Params{
			Def:     def,
			Cfg:     cfg,
			Doer:    doer,
			BaseURL: baseURLOf(inst, def),
			Clock:   r.clock,
			Logger:  r.log,
			PersistSetting: func(ctx context.Context, name, value string) error {
				return r.persistSetting(ctx, inst, def, name, value)
			},
		})
		if err != nil {
			return nil, fmt.Errorf("registry: build native driver %q: %w", def.ID, err)
		}
		return d, nil
	}
	opts := []cardigann.Option{
		cardigann.WithDoer(doer),
		cardigann.WithConfig(cfg),
		cardigann.WithClock(r.clock),
		// Wire an anti-bot solver from the instance settings ("solver_type" + the
		// encrypted "cookie"); a no-op when unset.
		cardigann.SolverOption(cfg),
	}
	if inst.BaseURL != "" {
		opts = append(opts, cardigann.WithBaseURL(inst.BaseURL))
	}
	eng, err := cardigann.NewEngine(def, opts...)
	if err != nil {
		return nil, fmt.Errorf("registry: build engine %q: %w", def.ID, err)
	}
	return eng, nil
}

// resolveDefinition resolves a definition id to its definition and, for a native family,
// its driver factory (nil for the Cardigann path). Native families are checked first, then
// the loader. It is a free function so both the serve path (Resolver.buildAdapter) and the
// CRUD path (Manager.Add/updateInTx) call it without either type owning it.
func resolveDefinition(fams map[string]native.Family, ldr *loader.Loader, id string) (*loader.Definition, native.Factory, error) {
	if fam, ok := fams[id]; ok {
		return fam.Definition, fam.Factory, nil
	}
	def, err := ldr.Load(id)
	if err != nil {
		return nil, nil, fmt.Errorf("registry: load definition %q: %w", id, err)
	}
	return def, nil, nil
}

// NativeDefinitions returns the Go-built definitions of the native families so the
// management API can list them as addable alongside the Cardigann corpus.
func (r *Resolver) NativeDefinitions() []*loader.Definition {
	out := make([]*loader.Definition, 0, len(r.native))
	for _, f := range r.native {
		out = append(out, f.Definition)
	}
	return out
}

// baseURLOf is an instance's effective base URL: its override, else the
// definition's first link.
func baseURLOf(inst domain.IndexerInstance, def *loader.Definition) string {
	if inst.BaseURL != "" {
		return inst.BaseURL
	}
	if len(def.Links) > 0 {
		return def.Links[0]
	}
	return ""
}

// decryptConfig turns stored settings into the engine's .Config map, decrypting
// each secret with the row-bound AAD.
func (r *Resolver) decryptConfig(instanceID int64, settings []domain.IndexerSetting) (map[string]string, error) {
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

// persistSetting durably writes a single (re-)encrypted setting back to the store for
// inst — the seam a native driver uses to persist a rotated credential (e.g.
// MyAnonamouse's mam_id). It deliberately does NOT invalidate the cache: the cached
// driver's in-memory value stays the live source, and this write only refreshes the
// restart fallback, so it cannot race a search by dropping the live session.
func (r *Resolver) persistSetting(ctx context.Context, inst domain.IndexerInstance, def *loader.Definition, name, value string) error {
	s, err := encodeSetting(r.keyring, inst.ID, name, value, settingFields(def))
	if err != nil {
		// The error carries the setting name, never the value.
		r.log.Warn().Str("indexer", inst.Slug).Str("setting", name).Err(err).Msg("registry: persist setting: encrypt failed")
		return err
	}
	if err := r.instances.UpsertSetting(ctx, r.db, inst.ID, s); err != nil {
		r.log.Warn().Str("indexer", inst.Slug).Str("setting", name).Err(err).Msg("registry: persist setting: write failed")
		return fmt.Errorf("registry: persist setting %q: %w", name, err)
	}
	return nil
}

// logResolveError logs a genuine resolve failure with the error redacted; a
// not-found or disabled instance is expected and stays quiet.
func (r *Resolver) logResolveError(slug string, err error) {
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, errDisabled) {
		return
	}
	r.log.Error().
		Str("indexer", slug).
		Str("error", apphttp.RedactError(err)).
		Msg("registry: resolve failed")
}

// invalidate drops a slug's cached engine so the next resolve rebuilds it. Bumping
// the slug's generation additionally rejects an engine still being built by an
// in-flight resolve (which has not yet cached it, so the delete alone is a no-op) —
// see resolve (U8R-F3).
//
// INVARIANT: every caller must invalidate AFTER the settings change is durably
// committed. The generation check only closes the mid-build race because the
// commit happens-before the bump, so a build that read pre-commit settings is
// guaranteed to see a stale generation. A caller that bumped before committing
// would reopen the race. The gen map is deliberately never pruned: dropping a
// slug's entry would reset it to 0 and reintroduce an ABA gap.
func (r *Resolver) invalidate(slug string) {
	r.mu.Lock()
	delete(r.cache, slug)
	r.gen[slug]++
	r.mu.Unlock()
}

// InvalidateAll drops every cached engine so the next resolve of each slug
// rebuilds it. Used after a global proxy/solver resource changes: buildAdapter
// bakes the resolved proxy/solver URL into the cached engine's transport, so a
// resource edit/delete must evict the engines that reference it. Finding the exact
// referencing slugs is possible but a full flush is cheaper to reason about for a
// rare, single-user config change, and only forces a lazy rebuild on next search.
func (r *Resolver) InvalidateAll() {
	r.mu.Lock()
	clear(r.cache)
	// A global epoch bump (rather than per-slug generations we don't enumerate here)
	// rejects every in-flight build, including builds for slugs not currently cached —
	// see resolve (U8R-F3).
	r.epoch++
	r.mu.Unlock()
}

// invalidateSearchCache purges the search-results cache entries for one instance
// after a config mutation. It is nil-guarded (a no-op when caching is off) and
// best-effort: a failed purge is logged (key/id only, never a payload) and never
// fails the mutation.
func (r *Resolver) invalidateSearchCache(ctx context.Context, instanceID int64) {
	if r.searchCache == nil {
		return
	}
	if _, err := r.searchCache.InvalidateByInstance(ctx, instanceID); err != nil {
		r.log.Warn().
			Int64("instance_id", instanceID).
			Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache invalidate failed")
	}
}

// forgetCacheCounters drops a deleted instance's in-memory cache counters so the
// global totals stay equal to the sum of the surviving rows and FlushCounters stops
// re-Upserting a cascade-deleted row. No-op when caching is off.
func (r *Resolver) forgetCacheCounters(instanceID int64) {
	if r.searchCache == nil {
		return
	}
	r.searchCache.ForgetInstance(instanceID)
}

// forgetStats drops a deleted instance's in-memory query/grab/latency counters, mirroring
// forgetCacheCounters for the durable stats layer. It is the fourth invalidator seam method
// the Manager calls after a committed Delete, keeping the Manager ignorant of *IndexerStats.
func (r *Resolver) forgetStats(instanceID int64) {
	r.stats.ForgetInstance(instanceID)
}

// indexerInfo assembles the public indexer identity from the instance + def (no
// secrets).
func indexerInfo(inst domain.IndexerInstance, def *loader.Definition) torznabhttp.IndexerInfo {
	site := inst.BaseURL
	if site == "" && len(def.Links) > 0 {
		site = def.Links[0]
	}
	return torznabhttp.IndexerInfo{
		ID:          inst.Slug,
		Name:        orDefault(inst.Name, def.Name),
		Description: def.Description,
		SiteLink:    site,
		Type:        def.Type,
		// Protocol is read from the persisted instance (the denormalized column),
		// not re-derived from the def, so the served identity matches what app-sync
		// reads and stays stable if a def is ever updated under an existing instance.
		Protocol: inst.Protocol,
	}
}

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
