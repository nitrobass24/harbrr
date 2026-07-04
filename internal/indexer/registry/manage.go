package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/secrets"
)

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

// UpdateParams is the input to Update. Nil Name/BaseURL leave those unchanged;
// Settings is merged into the existing set (a value of secrets.Redacted keeps the
// stored value; omitted settings are kept). ProxyID/SolverID are applied verbatim
// on every update (nil clears the reference) — the edit form always submits the
// full desired state via its resource dropdowns.
type UpdateParams struct {
	Name     *string
	BaseURL  *string
	Settings map[string]string
	ProxyID  *int64
	SolverID *int64
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
func (r *Registry) Add(ctx context.Context, p AddParams) (domain.IndexerInstance, error) {
	slug := orDefault(p.Slug, p.DefinitionID)
	if !slugPattern.MatchString(slug) {
		return domain.IndexerInstance{}, fmt.Errorf("%w: slug %q must be 1-64 chars of [a-z0-9._-] starting alphanumeric", ErrInvalid, slug)
	}
	def, _, err := r.definition(p.DefinitionID)
	if err != nil {
		return domain.IndexerInstance{}, fmt.Errorf("%w: unknown definition %q", ErrInvalid, p.DefinitionID)
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
		return r.writeSettings(ctx, tx, id, settingFields(def), p.Settings)
	})
	if err != nil {
		// ensureSlugFree is a pre-check; a concurrent Add can still lose the race
		// to the UNIQUE(slug) constraint. Map that to ErrConflict so conflict
		// semantics hold either way.
		if database.IsUniqueViolation(err) {
			return domain.IndexerInstance{}, fmt.Errorf("%w: indexer %q", ErrConflict, slug)
		}
		return domain.IndexerInstance{}, err
	}
	r.invalidate(slug)
	return inst, nil
}

// Get returns an instance and its settings with secret values redacted.
func (r *Registry) Get(ctx context.Context, slug string) (domain.IndexerInstance, []SettingView, error) {
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

// List returns all configured instances.
func (r *Registry) List(ctx context.Context) ([]domain.IndexerInstance, error) {
	list, err := r.instances.List(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("registry: list: %w", err)
	}
	return list, nil
}

// Update merges new settings into an instance (secrets.Redacted keeps the stored
// value; omitted settings are kept) and updates its name/base URL, atomically.
func (r *Registry) Update(ctx context.Context, slug string, p UpdateParams) error {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return fmt.Errorf("registry: update %q: %w", slug, err)
	}
	existing, err := r.instances.Settings(ctx, r.db, inst.ID)
	if err != nil {
		return fmt.Errorf("registry: update %q settings: %w", slug, err)
	}
	def, _, err := r.definition(inst.DefinitionID)
	if err != nil {
		return err
	}
	merged, err := r.mergeSettings(inst.ID, settingFields(def), existing, p.Settings)
	if err != nil {
		return err
	}

	name, baseURL := applyMeta(inst, p)
	err = r.inTx(ctx, func(tx dbinterface.TxQuerier) error {
		if err := r.instances.UpdateMeta(ctx, tx, inst.ID, name, baseURL, r.clock()); err != nil {
			return fmt.Errorf("registry: update meta: %w", err)
		}
		if err := r.instances.SetRefs(ctx, tx, inst.ID, p.ProxyID, p.SolverID, r.clock()); err != nil {
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
	})
	if err != nil {
		return err
	}
	r.invalidate(slug)
	r.invalidateSearchCache(ctx, inst.ID)
	return nil
}

// SetEnabled enables/disables an instance and invalidates its cached engine. It
// loads the instance first to obtain its id for the search-cache purge (a config
// change must never serve stale results).
func (r *Registry) SetEnabled(ctx context.Context, slug string, enabled bool) error {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return fmt.Errorf("registry: set enabled %q: %w", slug, err)
	}
	if err := r.instances.SetEnabled(ctx, r.db, slug, enabled, r.clock()); err != nil {
		return fmt.Errorf("registry: set enabled %q: %w", slug, err)
	}
	r.invalidate(slug)
	r.invalidateSearchCache(ctx, inst.ID)
	return nil
}

// Delete removes an instance (settings cascade) and invalidates its cached engine. It
// loads the instance first to obtain its id, so the in-memory cache counters can be
// pruned to match the cache_counters row the FK cascade removes.
func (r *Registry) Delete(ctx context.Context, slug string) error {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return fmt.Errorf("registry: delete %q: %w", slug, err)
	}
	if err := r.instances.Delete(ctx, r.db, slug); err != nil {
		return fmt.Errorf("registry: delete %q: %w", slug, err)
	}
	r.invalidate(slug)
	r.forgetCacheCounters(inst.ID)
	r.stats.ForgetInstance(inst.ID)
	return nil
}

// ensureSlugFree returns ErrConflict if the slug is taken.
func (r *Registry) ensureSlugFree(ctx context.Context, slug string) error {
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
func (r *Registry) writeSettings(ctx context.Context, tx dbinterface.TxQuerier, id int64, fields map[string]loader.SettingsField, settings map[string]string) error {
	for name, val := range settings {
		s, err := r.toStored(id, name, val, fields)
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
func (r *Registry) mergeSettings(id int64, fields map[string]loader.SettingsField, existing []domain.IndexerSetting, incoming map[string]string) ([]domain.IndexerSetting, error) {
	byName := make(map[string]domain.IndexerSetting, len(existing)+len(incoming))
	for _, s := range existing {
		byName[s.Name] = s
	}
	for name, val := range incoming {
		if secrets.IsRedacted(val) {
			continue // keep whatever is stored (or nothing, if unset)
		}
		s, err := r.toStored(id, name, val, fields)
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

// toStored classifies a setting and, if secret, encrypts its value bound to the
// instance id + setting name.
func (r *Registry) toStored(id int64, name, val string, fields map[string]loader.SettingsField) (domain.IndexerSetting, error) {
	if !classifySecret(name, fields) {
		return domain.IndexerSetting{Name: name, Value: val}, nil
	}
	blob, err := r.keyring.Encrypt(id, name, val)
	if err != nil {
		return domain.IndexerSetting{}, fmt.Errorf("registry: encrypt setting %q: %w", name, err)
	}
	return domain.IndexerSetting{Name: name, ValueEncrypted: blob, KeyID: r.keyring.KeyID(), IsSecret: true}, nil
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
func (r *Registry) Test(ctx context.Context, slug string) error {
	a, err := r.buildAdapter(ctx, slug)
	if err != nil {
		return err
	}
	if err := a.inner.Test(ctx); err != nil {
		a.recordHealth(ctx, err)
		return fmt.Errorf("registry: test %q: %w", slug, err)
	}
	return nil
}

// healthEventLimit caps how many recent events the status endpoint returns.
const healthEventLimit = 20

// healthRecencyWindow is how recently a failure must have occurred for the derived
// status to read "unhealthy"; older failures are treated as past (status healthy).
const healthRecencyWindow = 1 * time.Hour

// HealthStatus is one indexer's derived health plus the recent events behind it
// (details already credential-scrubbed at write time).
type HealthStatus struct {
	Slug   string
	Status string
	Events []domain.IndexerHealthEvent
}

// Status returns the indexer's derived health and recent events. An unknown slug
// is database.ErrNotFound (the handler maps it to 404).
func (r *Registry) Status(ctx context.Context, slug string) (HealthStatus, error) {
	inst, err := r.instances.GetBySlug(ctx, r.db, slug)
	if err != nil {
		return HealthStatus{}, fmt.Errorf("registry: status %q: %w", slug, err)
	}
	events, err := r.health.Recent(ctx, r.db, inst.ID, healthEventLimit)
	if err != nil {
		return HealthStatus{}, fmt.Errorf("registry: status events %q: %w", slug, err)
	}
	return HealthStatus{Slug: slug, Status: r.deriveStatus(events), Events: events}, nil
}

// deriveStatus reads "unhealthy" when the most recent event is within the recency
// window, else "healthy" (no recent failure). Events are newest-first.
func (r *Registry) deriveStatus(events []domain.IndexerHealthEvent) string {
	if len(events) > 0 && r.clock().Sub(events[0].OccurredAt) <= healthRecencyWindow {
		return "unhealthy"
	}
	return "healthy"
}

// IndexerFailureCounts is one indexer's failure tally by health kind, folded in from
// the append-only health events.
type IndexerFailureCounts struct {
	AuthFailure int64
	RateLimited int64
	ParseError  int64
	AntiBot     int64
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
func (r *Registry) Stats(ctx context.Context, slug string) (IndexerStat, error) {
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
func (r *Registry) AllStats(ctx context.Context) ([]IndexerStat, error) {
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
		},
		LastQueryAt:   lastQuery,
		LastFailureAt: counts.LastFailureAt,
	}
}

// RehydrateStats folds the persisted per-indexer counters onto the in-memory atomics at
// boot (a thin delegator to the stats layer for cmd/harbrr wiring).
func (r *Registry) RehydrateStats(ctx context.Context) error {
	return r.stats.RehydrateCounters(ctx)
}

// FlushStats writes the live per-indexer counters back to the store (a thin delegator
// for the periodic + shutdown flush in cmd/harbrr).
func (r *Registry) FlushStats(ctx context.Context) {
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
