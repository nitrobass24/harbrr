package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
	"github.com/autobrr/harbrr/internal/secrets"
)

// Manager is the transactional CRUD half of the registry: Add/Update/Delete plus the read
// accessors. Its consistency comes from the DB transaction (inTx), not an in-memory lock;
// after a committed mutation it evicts the serve path through the invalidator seam. It
// holds only the handles its methods use — no resolve cache, no *IndexerStats.
type Manager struct {
	db        dbinterface.Querier
	instances database.Instances
	keyring   secretsKeyring
	clock     func() time.Time
	loader    *loader.Loader
	native    map[string]native.Family
	inv       invalidator
}

// invalidator is the eviction surface the Manager depends on: after a committed mutation it
// drops the resolver's cached engine and purges the affected instance's derived state
// (search-cache entries + cache counters + query/grab stats). Satisfied structurally by
// *Resolver; declared here (consumer-side, UNEXPORTED) so the Manager depends only on
// eviction — never on the resolve/build engine — and the eviction methods stay off the
// public API (only InvalidateAll is exported).
type invalidator interface {
	invalidate(slug string)
	invalidateSearchCache(ctx context.Context, id int64)
	forgetCacheCounters(id int64)
	forgetStats(id int64)
	forgetBudget(id int64)
}

// StatsReporter is the health/stats reporting + lifecycle half of the registry: a read and
// flush view over the already-safe IndexerStats and the health store. It touches no resolve
// lock and holds no CRUD/serve state.
type StatsReporter struct {
	stats     *IndexerStats
	instances database.Instances
	health    database.Health
	circuit   database.Circuit
	db        dbinterface.Querier
	clock     func() time.Time
}

// Management-layer sentinels the API maps to HTTP status codes (400/409/404).
var (
	// ErrInvalid marks bad input (e.g. a malformed slug).
	ErrInvalid = errors.New("registry: invalid request")
	// ErrConflict marks a slug already in use.
	ErrConflict = errors.New("registry: already exists")
)

// slugPattern restricts a slug to a URL-safe, filename-safe identifier so it is
// a clean Torznab path segment and management resource id.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// reservedSlugs are slugs that must not name an indexer because they collide with
// a static path segment registered as a sibling of /api/indexers/{slug} in
// internal/web/api/router.go. chi prioritizes a static segment over the {slug}
// param, so an indexer slugged "stats" or "status" would be shadowed by GET
// /api/indexers/stats (allIndexerStats) or /api/indexers/status
// (allIndexerStatus). Keep this in sync with the static segments registered
// directly under /api/indexers/ in router.go.
var reservedSlugs = map[string]struct{}{
	"stats":  {},
	"status": {},
}

// AddParams is the input to Add. Slug defaults to DefinitionID when empty; Name
// defaults to the definition's name; Settings is the user's setting values keyed
// by setting name (secrets are encrypted on write).
type AddParams struct {
	Slug         string
	DefinitionID string
	Name         string
	BaseURL      string
	Settings     map[string]string
	// ProxyID / SolverID reference the global proxy / solver resources this indexer
	// uses (nil = none). The foreign key rejects a non-existent id.
	ProxyID  *int64
	SolverID *int64
}

// RefUpdate is a tri-state PATCH field for a nullable resource reference: Present
// false leaves the stored reference unchanged; Present true with a nil Value clears
// it; Present true with a value sets it. This keeps a partial PATCH (e.g. renaming
// an indexer) from silently clearing its proxy/solver reference.
type RefUpdate struct {
	Present bool
	Value   *int64
}

// UpdateParams is the input to Update. Nil Name/BaseURL leave those unchanged;
// Settings is merged into the existing set (a value of secrets.Redacted keeps the
// stored value; omitted settings are kept). ProxyID/SolverID are tri-state
// (RefUpdate): only an explicitly-present field changes the reference.
type UpdateParams struct {
	Name     *string
	BaseURL  *string
	Settings map[string]string
	ProxyID  RefUpdate
	SolverID RefUpdate
}

// SettingView is the API-safe representation of a stored setting: a secret's value
// is the <redacted> sentinel, never the plaintext.
type SettingView struct {
	Name   string
	Value  string
	Secret bool
}

// Add persists a new indexer instance and its settings atomically (the instance
// is inserted first so its id can bind each secret's AAD), then invalidates any
// cached engine for the slug.
func (r *Manager) Add(ctx context.Context, p AddParams) (domain.IndexerInstance, error) {
	slug := orDefault(p.Slug, p.DefinitionID)
	if !slugPattern.MatchString(slug) {
		return domain.IndexerInstance{}, fmt.Errorf("%w: slug %q must be 1-64 chars of [a-z0-9._-] starting alphanumeric", ErrInvalid, slug)
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return domain.IndexerInstance{}, fmt.Errorf("%w: slug %q is reserved", ErrInvalid, slug)
	}
	def, _, err := resolveDefinition(r.native, r.loader, p.DefinitionID)
	if err != nil {
		return domain.IndexerInstance{}, fmt.Errorf("%w: unknown definition %q", ErrInvalid, p.DefinitionID)
	}
	fields := settingFields(def)
	if err := validateRequiredSettings(fields, p.Settings); err != nil {
		return domain.IndexerInstance{}, err
	}
	if err := r.ensureSlugFree(ctx, slug); err != nil {
		return domain.IndexerInstance{}, err
	}

	now := r.clock()
	inst := domain.IndexerInstance{
		Slug: slug, DefinitionID: p.DefinitionID, Name: orDefault(p.Name, def.Name),
		BaseURL: p.BaseURL, Enabled: true, Protocol: def.EffectiveProtocol(),
		ProxyID: p.ProxyID, SolverID: p.SolverID,
		CreatedAt: now, UpdatedAt: now,
	}

	err = r.inTx(ctx, func(tx dbinterface.TxQuerier) error {
		id, err := r.instances.Insert(ctx, tx, inst)
		if err != nil {
			return fmt.Errorf("registry: insert instance: %w", err)
		}
		inst.ID = id
		return r.writeSettings(ctx, tx, id, fields, p.Settings)
	})
	if err != nil {
		// ensureSlugFree is a pre-check; a concurrent Add can still lose the race
		// to the UNIQUE(slug) constraint. Map that to ErrConflict so conflict
		// semantics hold either way.
		if database.IsUniqueViolation(err) {
			return domain.IndexerInstance{}, fmt.Errorf("%w: indexer %q", ErrConflict, slug)
		}
		// A dangling proxy_id/solver_id trips the FK constraint (foreign_keys=ON):
		// the client referenced a proxy/solver that does not exist, so it is invalid
		// input (400), not an internal error.
		if database.IsForeignKeyViolation(err) {
			return domain.IndexerInstance{}, fmt.Errorf("%w: unknown proxy or solver reference", ErrInvalid)
		}
		return domain.IndexerInstance{}, err
	}
	r.inv.invalidate(slug)
	return inst, nil
}

// Get returns an instance and its settings with secret values redacted.
func (r *Manager) Get(ctx context.Context, slug string) (domain.IndexerInstance, []SettingView, error) {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return domain.IndexerInstance{}, nil, fmt.Errorf("registry: get %q: %w", slug, err)
	}
	settings, err := r.instances.Settings(ctx, r.db, inst.ID)
	if err != nil {
		return domain.IndexerInstance{}, nil, fmt.Errorf("registry: get settings for %q: %w", slug, err)
	}
	views := make([]SettingView, 0, len(settings))
	for _, s := range settings {
		value := s.Value
		if s.IsSecret {
			value = secrets.Redacted
		}
		views = append(views, SettingView{Name: s.Name, Value: value, Secret: s.IsSecret})
	}
	return inst, views, nil
}

// Freeleech reports whether inst's freeleech-only checkbox is enabled, using the same
// canonical rule buildAdapter applies at engine-build time: canonicalize the stored
// checkbox value, then read it via settingEnabled, which stays hardened even if a value
// slips past canonicalization (registry.go, autobrr/harbrr#273). Cardigann defs name the
// checkbox "freeleech"; the native drivers (avistaz, filelist, gazellegames, iptorrents,
// torrentday) name theirs "freeleech_only" — same signal, both matched (#227). The
// setting is never a secret, so this reads its raw stored value directly — no decryption
// pass needed — keeping the list-time seam cheap.
func (r *Manager) Freeleech(ctx context.Context, inst domain.IndexerInstance) (bool, error) {
	def, _, err := resolveDefinition(r.native, r.loader, inst.DefinitionID)
	if err != nil {
		return false, fmt.Errorf("registry: load definition %q: %w", inst.DefinitionID, err)
	}
	settings, err := r.instances.Settings(ctx, r.db, inst.ID)
	if err != nil {
		return false, fmt.Errorf("registry: get settings for %q: %w", inst.Slug, err)
	}
	cfg := make(map[string]string, 1)
	for _, s := range settings {
		if s.Name == "freeleech" || s.Name == "freeleech_only" {
			cfg[s.Name] = s.Value
			break
		}
	}
	cardigann.CanonicalizeCheckboxes(def, cfg)
	return settingEnabled(cfg["freeleech"]) || settingEnabled(cfg["freeleech_only"]), nil
}

// List returns all configured instances.
func (r *Manager) List(ctx context.Context) ([]domain.IndexerInstance, error) {
	list, err := r.instances.List(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("registry: list: %w", err)
	}
	return list, nil
}

// Update merges new settings into an instance (secrets.Redacted keeps the stored
// value; omitted settings are kept) and updates its name/base URL, atomically.
// The whole read-modify-write — read current settings, merge the patch, then
// delete+reinsert — runs inside one transaction (reading via the tx handle, not
// r.db), so a concurrent persistSetting rotating a live credential (e.g. a native
// driver refreshing MyAnonamouse's mam_id) can't be clobbered by this write
// reinserting a stale merged set. SetMaxOpenConns(1) means the tx holds the only
// connection, serializing the RMW against that Upsert (mirrors appsync U10-F1).
func (r *Manager) Update(ctx context.Context, slug string, p UpdateParams) error {
	var instID int64
	err := r.inTx(ctx, func(tx dbinterface.TxQuerier) error {
		inst, err := r.instances.GetBySlug(ctx, tx, slug)
		if err != nil {
			return fmt.Errorf("registry: update %q: %w", slug, err)
		}
		instID = inst.ID
		return r.updateInTx(ctx, tx, inst, p)
	})
	if err != nil {
		// A dangling proxy_id/solver_id trips the FK constraint on SetRefs
		// (foreign_keys=ON): the client referenced a proxy/solver that does not
		// exist, so it is invalid input (400), not an internal error.
		if database.IsForeignKeyViolation(err) {
			return fmt.Errorf("%w: unknown proxy or solver reference", ErrInvalid)
		}
		return err
	}
	r.inv.invalidate(slug)
	r.inv.invalidateSearchCache(ctx, instID)
	return nil
}

// updateInTx applies the settings merge and metadata/ref updates for inst inside
// the caller's transaction. The current settings are read (via tx) and merged
// here — inside the tx — so the read → merge → delete → reinsert is one atomic
// unit that can't lose a concurrent single-setting Upsert. mergeSettings only
// touches the keyring (no DB), so it is safe within the tx.
func (r *Manager) updateInTx(ctx context.Context, tx dbinterface.TxQuerier, inst domain.IndexerInstance, p UpdateParams) error {
	existing, err := r.instances.Settings(ctx, tx, inst.ID)
	if err != nil {
		return fmt.Errorf("registry: update %q settings: %w", inst.Slug, err)
	}
	def, _, err := resolveDefinition(r.native, r.loader, inst.DefinitionID)
	if err != nil {
		return err
	}
	merged, err := r.mergeSettings(inst.ID, settingFields(def), existing, p.Settings)
	if err != nil {
		return err
	}
	cfg, err := decryptConfig(r.keyring, inst.ID, merged)
	if err != nil {
		return err
	}
	if err := validateRequiredSettings(settingFields(def), cfg); err != nil {
		return err
	}

	name, baseURL := applyMeta(inst, p)
	if err := r.instances.UpdateMeta(ctx, tx, inst.ID, name, baseURL, r.clock()); err != nil {
		return fmt.Errorf("registry: update meta: %w", err)
	}
	// Only a present ref field changes the stored reference; an absent one keeps
	// the instance's current value (so a partial PATCH can't clear it).
	proxyRef := resolveRef(p.ProxyID, inst.ProxyID)
	solverRef := resolveRef(p.SolverID, inst.SolverID)
	if err := r.instances.SetRefs(ctx, tx, inst.ID, proxyRef, solverRef, r.clock()); err != nil {
		return fmt.Errorf("registry: update refs: %w", err)
	}
	if err := r.instances.DeleteSettings(ctx, tx, inst.ID); err != nil {
		return fmt.Errorf("registry: clear settings: %w", err)
	}
	for _, s := range merged {
		if err := r.instances.InsertSetting(ctx, tx, inst.ID, s); err != nil {
			return fmt.Errorf("registry: write setting %q: %w", s.Name, err)
		}
	}
	return nil
}

// SetEnabled enables/disables an instance and invalidates its cached engine. It
// loads the instance first to obtain its id for the search-cache purge (a config
// change must never serve stale results).
func (r *Manager) SetEnabled(ctx context.Context, slug string, enabled bool) error {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return fmt.Errorf("registry: set enabled %q: %w", slug, err)
	}
	if err := r.instances.SetEnabled(ctx, r.db, slug, enabled, r.clock()); err != nil {
		return fmt.Errorf("registry: set enabled %q: %w", slug, err)
	}
	r.inv.invalidate(slug)
	r.inv.invalidateSearchCache(ctx, inst.ID)
	return nil
}

// Delete removes an instance (settings cascade) and invalidates its cached engine. It
// loads the instance first to obtain its id, so the in-memory cache counters can be
// pruned to match the cache_counters row the FK cascade removes.
func (r *Manager) Delete(ctx context.Context, slug string) error {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return fmt.Errorf("registry: delete %q: %w", slug, err)
	}
	if err := r.instances.Delete(ctx, r.db, slug); err != nil {
		return fmt.Errorf("registry: delete %q: %w", slug, err)
	}
	r.inv.invalidate(slug)
	r.inv.forgetCacheCounters(inst.ID)
	r.inv.forgetStats(inst.ID)
	r.inv.forgetBudget(inst.ID)
	return nil
}

// ensureSlugFree returns ErrConflict if the slug is taken.
func (r *Manager) ensureSlugFree(ctx context.Context, slug string) error {
	_, err := r.instances.GetBySlug(ctx, r.db, slug)
	switch {
	case err == nil:
		return fmt.Errorf("%w: indexer %q", ErrConflict, slug)
	case errors.Is(err, database.ErrNotFound):
		return nil
	default:
		return fmt.Errorf("registry: check slug %q: %w", slug, err)
	}
}

// writeSettings classifies and persists each new setting (encrypting secrets).
func (r *Manager) writeSettings(ctx context.Context, tx dbinterface.TxQuerier, id int64, fields map[string]loader.SettingsField, settings map[string]string) error {
	for name, val := range settings {
		s, err := encodeSetting(r.keyring, id, name, val, fields)
		if err != nil {
			return err
		}
		if err := r.instances.InsertSetting(ctx, tx, id, s); err != nil {
			return fmt.Errorf("registry: write setting %q: %w", s.Name, err)
		}
	}
	return nil
}

// mergeSettings overlays incoming values onto the existing set: a Redacted value
// keeps the stored row, any other value is (re)classified and (re)encrypted, and
// settings absent from incoming are preserved.
func (r *Manager) mergeSettings(id int64, fields map[string]loader.SettingsField, existing []domain.IndexerSetting, incoming map[string]string) ([]domain.IndexerSetting, error) {
	byName := make(map[string]domain.IndexerSetting, len(existing)+len(incoming))
	for _, s := range existing {
		byName[s.Name] = s
	}
	for name, val := range incoming {
		if secrets.IsRedacted(val) {
			continue // keep whatever is stored (or nothing, if unset)
		}
		s, err := encodeSetting(r.keyring, id, name, val, fields)
		if err != nil {
			return nil, err
		}
		byName[name] = s
	}
	out := make([]domain.IndexerSetting, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	return out, nil
}

// inTx runs fn inside a transaction, committing on success and rolling back on
// error. The repo methods fn calls take an Execer, which the TxQuerier satisfies,
// so an instance and its settings are written atomically. It is the Manager's
// transactional helper (the CRUD writes are the only transactional path).
func (r *Manager) inTx(ctx context.Context, fn func(tx dbinterface.TxQuerier) error) error {
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

// encodeSetting classifies a setting and, if secret, encrypts its value bound to the
// instance id + setting name. A free function so both the serve write path
// (Resolver.persistSetting) and the CRUD write path (Manager.writeSettings/mergeSettings)
// call it without either type owning it.
func encodeSetting(kr secretsKeyring, id int64, name, val string, fields map[string]loader.SettingsField) (domain.IndexerSetting, error) {
	if !classifySecret(name, fields) {
		return domain.IndexerSetting{Name: name, Value: val}, nil
	}
	blob, err := kr.Encrypt(id, name, val)
	if err != nil {
		return domain.IndexerSetting{}, fmt.Errorf("registry: encrypt setting %q: %w", name, err)
	}
	return domain.IndexerSetting{Name: name, ValueEncrypted: blob, KeyID: kr.KeyID(), IsSecret: true}, nil
}

// reservedSecretSettings are daemon-level settings (not declared in vendored
// definitions) whose values are credential-bearing and must always be encrypted
// at rest — e.g. a proxy URL may embed user:pass.
var reservedSecretSettings = map[string]struct{}{
	"proxy_url":        {},
	"flaresolverr_url": {},
}

// classifySecret decides whether a setting is secret: a reserved daemon secret key
// always is; otherwise the definition's field decides, falling back to a text-typed
// name match (so an undeclared credential-shaped setting is still encrypted).
func classifySecret(name string, fields map[string]loader.SettingsField) bool {
	if _, ok := reservedSecretSettings[name]; ok {
		return true
	}
	if f, ok := fields[name]; ok {
		return f.IsSecret()
	}
	return loader.SettingsField{Type: "text", Name: name}.IsSecret()
}

// Test builds a fresh, UNCACHED engine for slug and validates its configured
// credentials via the login probe. The ephemeral engine and its cookie jar are
// discarded, so any cached production engine and its live session are untouched.
// Returns nil when the credentials authenticate; otherwise the engine's login
// error (which the API layer sanitizes before returning to the client).
func (r *Resolver) Test(ctx context.Context, slug string) error {
	a, err := r.buildAdapter(ctx, slug)
	if err != nil {
		return err
	}
	if err := a.inner.Test(ctx); err != nil {
		a.recordHealth(ctx, err)
		return fmt.Errorf("registry: test %q: %w", slug, err)
	}
	if err := a.health.RecordRecovery(ctx, a.db, a.instanceID, a.clock()); err != nil {
		return fmt.Errorf("registry: record successful test for %q: %w", slug, err)
	}
	return nil
}

// healthEventLimit caps how many recent events the status endpoint returns.
const healthEventLimit = 20

// healthRecencyWindow is how recently a failure must have occurred for the derived
// status to read "unhealthy"; older failures are treated as past (status healthy).
const healthRecencyWindow = 1 * time.Hour

// HealthStatus is one indexer's derived health plus the recent events behind it
// (details already credential-scrubbed at write time). DisabledTill is non-nil
// while the circuit breaker (#253) currently excludes the indexer from dispatch.
type HealthStatus struct {
	Slug         string
	Status       string
	Events       []domain.IndexerHealthEvent
	DisabledTill *time.Time
}

// Status returns the indexer's derived health and recent events. An unknown slug
// is database.ErrNotFound (the handler maps it to 404).
func (r *StatsReporter) Status(ctx context.Context, slug string) (HealthStatus, error) {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return HealthStatus{}, fmt.Errorf("registry: status %q: %w", slug, err)
	}
	events, status, disabledTill, err := r.statusOf(ctx, inst.ID, healthEventLimit)
	if err != nil {
		return HealthStatus{}, fmt.Errorf("registry: status %q: %w", slug, err)
	}
	return HealthStatus{Slug: slug, Status: status, Events: events, DisabledTill: disabledTill}, nil
}

// FleetStatus is one indexer's derived health for the fleet-wide roll-up: the
// status plus its single most recent health event (Events is empty when it has
// none, mirroring HealthStatus.Events). DisabledTill mirrors HealthStatus.
type FleetStatus struct {
	Slug         string
	Status       string
	Events       []domain.IndexerHealthEvent
	DisabledTill *time.Time
}

// AllStatuses returns every configured instance's derived health, sorted by slug.
// Like Status, it derives from only the newest event per instance (deriveStatus
// reads events[0]), so it fetches with limit 1 rather than pulling healthEventLimit
// events per instance.
func (r *StatsReporter) AllStatuses(ctx context.Context) ([]FleetStatus, error) {
	list, err := r.instances.List(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("registry: all statuses: %w", err)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Slug < list[j].Slug })
	out := make([]FleetStatus, 0, len(list))
	for _, inst := range list {
		events, status, disabledTill, err := r.statusOf(ctx, inst.ID, 1)
		if err != nil {
			return nil, fmt.Errorf("registry: all statuses %q: %w", inst.Slug, err)
		}
		out = append(out, FleetStatus{Slug: inst.Slug, Status: status, Events: events, DisabledTill: disabledTill})
	}
	return out, nil
}

// statusOf is the shared derivation core behind Status and AllStatuses: it fetches
// the instance's most recent events (capped at limit), its recovery marker, and its
// circuit-breaker state, and derives the status exactly the same way for both
// callers. The returned *time.Time is non-nil only while the circuit is open.
func (r *StatsReporter) statusOf(ctx context.Context, instanceID int64, limit int) ([]domain.IndexerHealthEvent, string, *time.Time, error) {
	events, err := r.health.Recent(ctx, r.db, instanceID, limit)
	if err != nil {
		return nil, "", nil, fmt.Errorf("events: %w", err)
	}
	recovery, err := r.health.Recovery(ctx, r.db, instanceID)
	if err != nil {
		return nil, "", nil, fmt.Errorf("recovery: %w", err)
	}
	circuit, err := r.circuit.Get(ctx, r.db, instanceID)
	if err != nil {
		return nil, "", nil, fmt.Errorf("circuit: %w", err)
	}
	now := r.clock()
	var disabledTill *time.Time
	if circuit.IsDisabled(now) {
		till := circuit.DisabledTill
		disabledTill = &till
	}
	return events, r.deriveStatus(events, recovery, disabledTill != nil), disabledTill, nil
}

// deriveStatus reads "unhealthy" when either the circuit breaker currently excludes
// the indexer from dispatch (disabled, #253 — this can outlast healthRecencyWindow
// on a high escalation rung, e.g. a 24h disable well past its 1h-old triggering
// event) or the most recent event is within the recency window and happened after
// the last successful explicit test. Events are newest-first.
func (r *StatsReporter) deriveStatus(events []domain.IndexerHealthEvent, recovery database.HealthRecovery, disabled bool) string {
	if disabled {
		return "unhealthy"
	}
	if len(events) > 0 && failureAfterRecovery(events[0], recovery) &&
		r.clock().Sub(events[0].OccurredAt) <= healthRecencyWindow {
		return "unhealthy"
	}
	return "healthy"
}

// failureAfterRecovery uses the monotonic event id in the normal case. OccurredAt
// covers a later event whose id was reused after retention emptied the event table.
func failureAfterRecovery(event domain.IndexerHealthEvent, recovery database.HealthRecovery) bool {
	if recovery.OccurredAt.IsZero() {
		return true
	}
	return event.ID > recovery.ThroughEventID || event.OccurredAt.After(recovery.OccurredAt)
}

// IndexerFailureCounts is one indexer's failure tally by health kind, folded in from
// the append-only health events.
type IndexerFailureCounts struct {
	AuthFailure int64
	RateLimited int64
	ParseError  int64
	AntiBot     int64
	Transport   int64
}

// IndexerStat is one indexer's Prowlarr-style stats: the durable query/grab/latency
// counters plus the failure aggregation and the last-query/last-failure times.
// AvgResponseMs is derived (response-time total / queries), so it is 0 when the indexer
// has never been queried. LastQueryAt/LastFailureAt are zero when never observed.
type IndexerStat struct {
	Slug          string
	Queries       int64
	Grabs         int64
	AvgResponseMs int64
	Failures      IndexerFailureCounts
	LastQueryAt   time.Time
	LastFailureAt time.Time
}

// Stats returns one indexer's per-indexer stats: its durable counters plus the failure
// aggregation from the health events. An unknown slug is database.ErrNotFound (the
// handler maps it to 404). Note the query count reflects searches that actually reached
// the tracker — a cache hit bypasses the instrumented adapter — so avgResponseMs
// measures real upstream latency.
func (r *StatsReporter) Stats(ctx context.Context, slug string) (IndexerStat, error) {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return IndexerStat{}, fmt.Errorf("registry: stats %q: %w", slug, err)
	}
	counts, err := r.health.Counts(ctx, r.db, inst.ID)
	if err != nil {
		return IndexerStat{}, fmt.Errorf("registry: stats failures %q: %w", slug, err)
	}
	queries, grabs, respTotal, lastQuery, _ := r.stats.snapshot(inst.ID)
	return buildIndexerStat(slug, queries, grabs, respTotal, lastQuery, counts), nil
}

// AllStats returns per-indexer stats for every configured instance. It reads the
// failure aggregation for all instances in one query (no N+1) and folds each instance's
// durable counters on top.
func (r *StatsReporter) AllStats(ctx context.Context) ([]IndexerStat, error) {
	list, err := r.instances.List(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("registry: all stats: %w", err)
	}
	countsByInstance, err := r.health.AllCounts(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("registry: all stats failures: %w", err)
	}
	out := make([]IndexerStat, 0, len(list))
	for _, inst := range list {
		queries, grabs, respTotal, lastQuery, _ := r.stats.snapshot(inst.ID)
		out = append(out, buildIndexerStat(inst.Slug, queries, grabs, respTotal, lastQuery, countsByInstance[inst.ID]))
	}
	return out, nil
}

// buildIndexerStat assembles the public stat from the durable counters and the health
// aggregation, deriving the average response time (guarded against divide-by-zero).
func buildIndexerStat(slug string, queries, grabs, respTotal int64, lastQuery time.Time, counts database.HealthCounts) IndexerStat {
	var avg int64
	if queries > 0 {
		avg = respTotal / queries
	}
	return IndexerStat{
		Slug:          slug,
		Queries:       queries,
		Grabs:         grabs,
		AvgResponseMs: avg,
		Failures: IndexerFailureCounts{
			AuthFailure: counts.AuthFailure,
			RateLimited: counts.RateLimited,
			ParseError:  counts.ParseError,
			AntiBot:     counts.AntiBot,
			Transport:   counts.Transport,
		},
		LastQueryAt:   lastQuery,
		LastFailureAt: counts.LastFailureAt,
	}
}

// RehydrateStats folds the persisted per-indexer counters onto the in-memory atomics at
// boot (a thin delegator to the stats layer for cmd/harbrr wiring).
func (r *StatsReporter) RehydrateStats(ctx context.Context) error {
	return r.stats.RehydrateCounters(ctx)
}

// FlushStats writes the live per-indexer counters back to the store (a thin delegator
// for the periodic + shutdown flush in cmd/harbrr).
func (r *StatsReporter) FlushStats(ctx context.Context) {
	r.stats.FlushCounters(ctx)
}

// settingFields indexes a definition's settings by name.
func settingFields(def *loader.Definition) map[string]loader.SettingsField {
	m := make(map[string]loader.SettingsField, len(def.Settings))
	for _, s := range def.Settings {
		m[s.Name] = s
	}
	return m
}

// validateRequiredSettings rejects missing, empty, whitespace-only, and
// redacted-placeholder values for definition fields marked required — a client
// echoing the <redacted> read-back sentinel must never persist it as the secret.
func validateRequiredSettings(fields map[string]loader.SettingsField, settings map[string]string) error {
	for name, field := range fields {
		if !field.Required {
			continue
		}
		value := settings[name]
		if strings.TrimSpace(value) == "" || secrets.IsRedacted(value) {
			return fmt.Errorf("%w: setting %q is required", ErrInvalid, name)
		}
	}
	return nil
}

// applyMeta resolves the post-update name and base URL from the optional params.
func applyMeta(inst domain.IndexerInstance, p UpdateParams) (name, baseURL string) {
	name, baseURL = inst.Name, inst.BaseURL
	if p.Name != nil {
		name = *p.Name
	}
	if p.BaseURL != nil {
		baseURL = *p.BaseURL
	}
	return name, baseURL
}

// resolveRef applies a tri-state reference update: a present update wins (its
// value, nil to clear); an absent one keeps the instance's current reference.
func resolveRef(update RefUpdate, current *int64) *int64 {
	if update.Present {
		return update.Value
	}
	return current
}
