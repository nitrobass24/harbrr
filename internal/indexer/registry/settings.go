package registry

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// Reserved instance settings the freeleech serve-time view reads. Cardigann defs
// name the checkbox "freeleech" (345 vendored defs declare it as their own setting,
// so the key is def-shared, unlike the purely-operational reserved keys below); the
// native drivers name theirs "freeleech_only" (#227).
const (
	freeleechSetting     = "freeleech"
	freeleechOnlySetting = "freeleech_only"
)

// instanceSettings is everything harbrr itself (not the definition) reads from an
// instance's stored settings. Built once at engine-build time by
// resolveInstanceSettings; no field is reassigned after buildAdapterAt publishes the
// adapter (applyWarmCapability runs before that), so readers need no synchronization
// — this replaces the old scheme where eight files each read a magic cfg key at
// serve time and buildAdapterAt mutated the settings map in a comment-enforced
// order.
type instanceSettings struct {
	// Timeout is the per-instance request timeout: the "timeout" setting when valid,
	// else the registry default.
	Timeout time.Duration
	// RateInterval is the effective per-host spacing (autobrr/harbrr#104): the
	// "rate_interval" override else the live global default, never below the
	// definition's own requestDelay floor. See resolveRateInterval.
	RateInterval time.Duration
	// CacheTTL is the per-instance "cache_ttl" override for the search cache;
	// 0 = no override (the global tier applies). See resolveTTL.
	CacheTTL time.Duration
	// WarmInterval is the clamped "rss_warm_interval" setting; 0 = opted out. It is
	// zeroed at build time for a driver the warmer always skips (applyWarmCapability),
	// so the TTL floor (warmFloor) and the warmer stay consistent by construction.
	WarmInterval time.Duration
	// Budget is the resolved per-indexer request-budget knobs (autobrr/harbrr#251).
	Budget budgetLimits
	// Freeleech is the serve-time freeleech view flag — the stored "freeleech"
	// setting, hardened via settingEnabled (autobrr/harbrr#273). The engine's map is
	// built WITHOUT the key when this is on (see resolveInstanceSettings), so every
	// fetch returns the full catalog and the view is applied on serve.
	Freeleech bool
	// engineCfg is the map the engine-shaped core consumes: the decrypted settings
	// with the referenced global proxy / solver resolved into the same keys the
	// inline settings use (proxy_type/proxy_url; solver_type/flaresolverr_url/
	// flaresolverr_max_timeout) — buildTransport and cardigann.SolverOption read
	// those keys off the map, so they must stay visible here — and with the
	// "freeleech" key cleared IFF Freeleech is on. It carries secrets and is never
	// logged. A native driver may write a rotated credential back into it (e.g.
	// gazellegames' fetched passkey), exactly as it did into the pre-refactor cfg.
	engineCfg map[string]string
}

// resolveInstanceSettings resolves an instance's decrypted settings map into the
// typed instanceSettings, in the order buildAdapterAt used to perform inline:
// resolve proxy/solver references into the map, canonicalize checkbox values, parse
// the reserved operational keys, then derive the engine's view of the map. raw must
// be a map nothing else holds a reference to yet (buildAdapterAt's decryptConfig
// result is fresh per build) — the ref resolution writes into it.
func (r *Resolver) resolveInstanceSettings(ctx context.Context, inst domain.IndexerInstance, def *loader.Definition, raw map[string]string) (instanceSettings, error) {
	// Resolve the instance's referenced global proxy / solver into the map BEFORE
	// anything reads it, so buildTransport / SolverOption stay unchanged (they only
	// read the map's keys). A reference wins over an inline setting; no reference
	// leaves the inline value (the fallback) in place.
	if err := r.resolveResourceRefs(ctx, inst, raw); err != nil {
		return instanceSettings{}, err
	}
	// Canonicalize checkbox settings ("True"/"") before anything reads them: a value
	// persisted as the literal "false" is non-empty and would otherwise read as CHECKED
	// under template truthiness (autobrr/harbrr#119).
	cardigann.CanonicalizeCheckboxes(def, raw)

	s := instanceSettings{
		Timeout:      resolveTimeout(raw, r.timeout),
		RateInterval: resolveRateInterval(def, raw, r.RateDefault()), // a native def carries RequestDelay, so it is paced too
		CacheTTL:     resolveCacheTTL(raw["cache_ttl"]),
		Budget:       resolveBudgetLimits(raw),
		Freeleech:    settingEnabled(raw[freeleechSetting]),
		engineCfg:    raw,
	}
	if wi, ok := warmIntervalFromValue(raw[warmIntervalSetting]); ok {
		s.WarmInterval = wi
	}
	// freeleech is consumed as a SERVE-TIME view, not a fetch-time filter: the engine
	// is built with the key cleared so every fetch returns the full catalog (cached
	// once and shared by the honor + bypass feeds). The Freeleech flag drives the
	// serve-time view in indexerAdapter.Search. Go-template truthiness alone is
	// "non-empty" (a checked box resolves to "True", config.go), so settingEnabled
	// hardens the read against every writer: a value ParseBool recognizes as false
	// (e.g. a persisted literal "false") reads as off even though it is non-empty
	// (autobrr/harbrr#273).
	if s.Freeleech {
		s.engineCfg = maps.Clone(raw)
		delete(s.engineCfg, freeleechSetting)
	}
	return s, nil
}

// applyWarmCapability zeroes WarmInterval when the built driver is one the warmer
// always skips (paging or Mode-consuming, per warmOne's own skip check): such a
// driver has no single canonical RSS cache key for the warmer to ever keep hot, so
// the setting's TTL-floor effect (warmFloor) would otherwise pin a consumer-driven
// RSS write-back to >=warmMinInterval with NO warmer EVER refreshing it — strictly
// staler data for zero freshness benefit, contradicting warmIntervalSetting's
// documented "no effect for these instances" contract. Called in buildAdapterAt
// after the inner driver is built (the capabilities are not knowable earlier) and
// BEFORE the adapter is published, so the settings are never mutated once another
// goroutine can see them.
func (s *instanceSettings) applyWarmCapability(pages, consumesMode bool) {
	if pages || consumesMode {
		s.WarmInterval = 0
	}
}

// resolveResourceRefs merges an instance's referenced global proxy / solver into
// raw, writing the same keys the inline settings use (proxy_type/proxy_url;
// solver_type/flaresolverr_url/flaresolverr_max_timeout) so buildTransport and
// SolverOption need no change. A reference overrides an inline value; no reference
// leaves the inline fallback in place. A dangling reference (the resource was
// deleted mid-flight, before the ON DELETE SET NULL fired) is skipped, not fatal —
// the indexer degrades to no proxy / no solver.
func (r *Resolver) resolveResourceRefs(ctx context.Context, inst domain.IndexerInstance, raw map[string]string) error {
	if inst.ProxyID != nil {
		p, err := r.proxies.GetProxy(ctx, r.db, *inst.ProxyID)
		switch {
		case err == nil:
			password, derr := r.keyring.Decrypt(p.ID, domain.ProxySecretPassword, p.PasswordEncrypted)
			if derr != nil {
				return fmt.Errorf("registry: decrypt proxy %d password: %w", p.ID, derr)
			}
			raw["proxy_type"], raw["proxy_url"] = p.Type, composeProxyURL(p, password)
		case !errors.Is(err, database.ErrNotFound):
			return fmt.Errorf("registry: load proxy %d: %w", *inst.ProxyID, err)
		}
	}
	if inst.SolverID != nil {
		s, err := r.solvers.GetSolver(ctx, r.db, *inst.SolverID)
		switch {
		case err == nil:
			flareURL, derr := r.keyring.Decrypt(s.ID, domain.SolverSecretURL, s.URLEncrypted)
			if derr != nil {
				return fmt.Errorf("registry: decrypt solver %d url: %w", s.ID, derr)
			}
			raw["solver_type"], raw["flaresolverr_url"] = s.Type, flareURL
			if s.MaxTimeout > 0 {
				raw["flaresolverr_max_timeout"] = strconv.Itoa(s.MaxTimeout)
			}
		case !errors.Is(err, database.ErrNotFound):
			return fmt.Errorf("registry: load solver %d: %w", *inst.SolverID, err)
		}
	}
	return nil
}

// composeProxyURL builds the type://[user[:pass]@]host:port transport URL
// buildTransport expects from a proxy's structured fields — only the password
// needed decrypting; host/port/username are already plain. This string can embed
// the password, so it lives only in the proxy_url setting for buildTransport's
// http.ProxyURL/proxy.FromURL call and is never logged, traced, or returned in an
// error (registry's own errors above name the proxy by id, never its URL).
func composeProxyURL(p domain.Proxy, password string) string {
	u := &url.URL{Scheme: p.Type, Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	switch {
	case password != "":
		u.User = url.UserPassword(p.Username, password)
	case p.Username != "":
		u.User = url.User(p.Username)
	}
	return u.String()
}

// resolveTimeout picks the per-instance request timeout: a "timeout" setting
// (Go duration, e.g. "30s") when present and valid, else the registry default.
func resolveTimeout(cfg map[string]string, fallback time.Duration) time.Duration {
	if v := cfg["timeout"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// defRequestDelay returns the definition's own requestDelay floor — a tracker
// requirement resolveRateInterval must never undercut — or 0 when the def declares
// none.
func defRequestDelay(def *loader.Definition) time.Duration {
	if def != nil && def.RequestDelay != nil && *def.RequestDelay > 0 {
		return time.Duration(*def.RequestDelay * float64(time.Second))
	}
	return 0
}

// resolveRateInterval picks the effective per-host spacing (autobrr/harbrr#104): the
// instance's "rate_interval" override (a reserved setting, like "timeout" — a Go
// duration string; invalid/non-positive is ignored) REPLACES the live global default
// when present — an operator setting one indexer faster than the global default is
// preference layering, not a floor violation. Either way, the definition's own
// requestDelay is a tracker-respect floor that always wins: the result is never
// below it, so user config can only slow harbrr down relative to the def, never
// speed it past what the tracker itself declared.
func resolveRateInterval(def *loader.Definition, cfg map[string]string, globalDefault time.Duration) time.Duration {
	user := globalDefault
	if v := cfg["rate_interval"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			user = d
		}
	}
	if floor := defRequestDelay(def); floor > user {
		return floor
	}
	return user
}

// resolveCacheTTL parses the per-instance "cache_ttl" override (a Go duration,
// e.g. "10m"). Absent, unparseable, or non-positive is 0 — no override, the cache's
// global tier applies (resolveTTL).
func resolveCacheTTL(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return 0
}

// warmIntervalFromValue parses and clamps a raw "rss_warm_interval" value. Absent
// (empty string), unparseable, or non-positive is disabled (ok=false) — opt-in,
// default-off. A parsed value is clamped to [warmMinInterval, warmMaxInterval].
// Shared by resolveInstanceSettings (the serve-path snapshot) and warmInterval
// (the warmer's per-tick read of the stored rows), so both apply the same clamp.
func warmIntervalFromValue(raw string) (time.Duration, bool) {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, false
	}
	switch {
	case d < warmMinInterval:
		return warmMinInterval, true
	case d > warmMaxInterval:
		return warmMaxInterval, true
	default:
		return d, true
	}
}

// budgetLimits is the resolved per-indexer request-budget configuration
// (autobrr/harbrr#251): the reserved query_limit/grab_limit/limits_unit settings
// plus the *_limit_source provenance markers (#377), parsed once by
// resolveBudgetLimits instead of per budget call off the settings map.
type budgetLimits struct {
	// unit is "day" (the default) or "hour", mirroring Prowlarr's limitsUnit.
	unit string
	// query/grab are the configured caps; nil = no cap configured (the budget is
	// disabled for that kind — only the reactive learning latch can still block it).
	query *int
	grab  *int
	// queryDetected/grabDetected report that the cap was read from the indexer's own
	// account limits rather than typed by the operator (autobrr/harbrr#377).
	queryDetected bool
	grabDetected  bool
}

// limitSourceDetected is the provenance marker a driver writes beside a cap it
// discovered from the indexer itself (the Newznab driver's ?t=user seed,
// autobrr/harbrr#377) — a plain reserved instance setting, the same generic
// mechanism the caps themselves ride, so no parallel store is needed to tell a
// detected cap from an operator-typed one.
const limitSourceDetected = "detected"

// resolveBudgetLimits reads the budget's reserved keys off a settings map. Callers:
// resolveInstanceSettings (the serve path's build-time snapshot) and the stats
// layer's budgetStatus (a fresh per-read view for the usage meter).
func resolveBudgetLimits(cfg map[string]string) budgetLimits {
	return budgetLimits{
		unit:          resolveLimitsUnit(cfg),
		query:         parseLimit(cfg["query_limit"]),
		grab:          parseLimit(cfg["grab_limit"]),
		queryDetected: cfg["query_limit_source"] == limitSourceDetected,
		grabDetected:  cfg["grab_limit_source"] == limitSourceDetected,
	}
}

// limit returns kind's configured cap (nil = none).
func (b budgetLimits) limit(kind budgetKind) *int {
	if kind == budgetKindGrab {
		return b.grab
	}
	return b.query
}

// detected reports whether kind's cap carries the detected-provenance marker.
func (b budgetLimits) detected(kind budgetKind) bool {
	if kind == budgetKindGrab {
		return b.grabDetected
	}
	return b.queryDetected
}

// resolveLimitsUnit reads the instance's limits_unit setting ("day" default, "hour"
// opt-in), mirroring Prowlarr's field name/semantics.
func resolveLimitsUnit(cfg map[string]string) string {
	if strings.EqualFold(strings.TrimSpace(cfg["limits_unit"]), "hour") {
		return "hour"
	}
	return "day"
}

// parseLimit parses a configured limit value (query_limit/grab_limit). An unset,
// non-numeric, or non-positive value returns nil ("no cap configured" — the
// safe/default posture per #251's corrected premise: unset means the budget is off
// for that kind, never an invented default).
func parseLimit(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

// settingEnabled reports whether a persisted checkbox-shaped setting value ("freeleech",
// "freeleech_only", or any future on/off setting read via a presence check) means ON.
// Go-template truthiness treats any non-empty .Config value as set (a checked box
// resolves to "True", cardigann/config.go's configTrue), so a naive `!= ""` check reads
// a persisted literal "false" as CHECKED — the box the operator explicitly unchecked
// (autobrr/harbrr#273). This hardens that read in one place, independent of whether
// CanonicalizeCheckboxes ran first, whether the def types the setting as a checkbox at
// all, or what a future writer persists for "off":
//   - "" (absent/cleared) -> false
//   - anything strconv.ParseBool recognizes ("true"/"1"/"0"/"t"/"f", any case, including
//     cardigann's "True" sentinel) -> its parsed value
//   - any other non-empty string (e.g. a def's exotic truthy default) -> true, preserving
//     Go-template truthiness for values ParseBool doesn't understand
func settingEnabled(v string) bool {
	if v == "" {
		return false
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return true
}
