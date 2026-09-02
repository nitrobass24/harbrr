package registry

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// settingsResolver is a bare Resolver carrying only what resolveInstanceSettings
// reads for an instance with no proxy/solver references: the registry default
// timeout and the live global rate default.
func settingsResolver(rateDefault time.Duration) *Resolver {
	r := &Resolver{timeout: defaultHTTPTimeout}
	r.rateDefault.Store(int64(rateDefault))
	return r
}

func intp(n int) *int { return &n }

// TestResolveInstanceSettings pins every reserved operational key's parse through
// the one constructor, with the same inputs the per-key resolve* helpers are (and
// were) tested with: default/valid/invalid/non-positive for each duration, the
// rate override-vs-def-floor rule, the warm clamp, and the budget knobs.
func TestResolveInstanceSettings(t *testing.T) {
	t.Parallel()
	globalRate := 2 * time.Second

	tests := []struct {
		name string
		def  *loader.Definition
		raw  map[string]string

		wantTimeout time.Duration
		wantRate    time.Duration
		wantTTL     time.Duration
		wantWarm    time.Duration
		wantUnit    string
		wantQuery   *int
		wantGrab    *int
		wantQueryD  bool
	}{
		{
			name:        "empty settings resolve to the defaults",
			raw:         map[string]string{},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
		{
			name:        "valid timeout override",
			raw:         map[string]string{"timeout": "30s"},
			wantTimeout: 30 * time.Second, wantRate: globalRate, wantUnit: "day",
		},
		{
			name:        "malformed / non-positive timeout falls back",
			raw:         map[string]string{"timeout": "nope"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
		{
			name:        "zero timeout falls back",
			raw:         map[string]string{"timeout": "0s"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
		{
			name:        "rate override replaces the global default",
			raw:         map[string]string{"rate_interval": "10s"},
			wantTimeout: defaultHTTPTimeout, wantRate: 10 * time.Second, wantUnit: "day",
		},
		{
			name:        "rate override below the def floor: floor wins, never undercut",
			def:         defWithDelay(5),
			raw:         map[string]string{"rate_interval": "500ms"},
			wantTimeout: defaultHTTPTimeout, wantRate: 5 * time.Second, wantUnit: "day",
		},
		{
			name:        "malformed rate override is ignored",
			raw:         map[string]string{"rate_interval": "not-a-duration"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
		{
			name:        "valid cache_ttl override",
			raw:         map[string]string{"cache_ttl": "10m"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantTTL: 10 * time.Minute, wantUnit: "day",
		},
		{
			name:        "invalid / zero / negative cache_ttl is no override",
			raw:         map[string]string{"cache_ttl": "-5m"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
		{
			name:        "warm interval mid-range as-is",
			raw:         map[string]string{"rss_warm_interval": "15m"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantWarm: 15 * time.Minute, wantUnit: "day",
		},
		{
			name:        "warm interval below floor clamps up",
			raw:         map[string]string{"rss_warm_interval": "5m"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantWarm: warmMinInterval, wantUnit: "day",
		},
		{
			name:        "warm interval above ceiling clamps down",
			raw:         map[string]string{"rss_warm_interval": "200m"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantWarm: warmMaxInterval, wantUnit: "day",
		},
		{
			name:        "unparseable warm interval is opted out",
			raw:         map[string]string{"rss_warm_interval": "abc"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
		{
			name: "budget caps, unit, and detected provenance",
			raw: map[string]string{
				"query_limit": "2000", "query_limit_source": "detected",
				"grab_limit": "10", "limits_unit": "hour",
			},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "hour",
			wantQuery: intp(2000), wantGrab: intp(10), wantQueryD: true,
		},
		{
			name:        "non-positive / non-numeric budget caps mean no cap",
			raw:         map[string]string{"query_limit": "0", "grab_limit": "x"},
			wantTimeout: defaultHTTPTimeout, wantRate: globalRate, wantUnit: "day",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def := tt.def
			if def == nil {
				def = &loader.Definition{}
			}
			s, err := settingsResolver(globalRate).resolveInstanceSettings(
				context.Background(), domain.IndexerInstance{}, def, tt.raw,
			)
			if err != nil {
				t.Fatalf("resolveInstanceSettings: %v", err)
			}
			if s.Timeout != tt.wantTimeout {
				t.Errorf("Timeout = %v, want %v", s.Timeout, tt.wantTimeout)
			}
			if s.RateInterval != tt.wantRate {
				t.Errorf("RateInterval = %v, want %v", s.RateInterval, tt.wantRate)
			}
			if s.CacheTTL != tt.wantTTL {
				t.Errorf("CacheTTL = %v, want %v", s.CacheTTL, tt.wantTTL)
			}
			if s.WarmInterval != tt.wantWarm {
				t.Errorf("WarmInterval = %v, want %v", s.WarmInterval, tt.wantWarm)
			}
			if s.Budget.unit != tt.wantUnit {
				t.Errorf("Budget.unit = %q, want %q", s.Budget.unit, tt.wantUnit)
			}
			checkLimit(t, "query", s.Budget.query, tt.wantQuery)
			checkLimit(t, "grab", s.Budget.grab, tt.wantGrab)
			if s.Budget.queryDetected != tt.wantQueryD {
				t.Errorf("Budget.queryDetected = %v, want %v", s.Budget.queryDetected, tt.wantQueryD)
			}
			if s.Freeleech {
				t.Error("Freeleech = true for settings with no freeleech key")
			}
		})
	}
}

func checkLimit(t *testing.T, kind string, got, want *int) {
	t.Helper()
	switch {
	case (got == nil) != (want == nil):
		t.Errorf("Budget.%s = %v, want %v", kind, got, want)
	case got != nil && *got != *want:
		t.Errorf("Budget.%s = %d, want %d", kind, *got, *want)
	}
}

// TestResolveInstanceSettingsFreeleech pins the freeleech conditional both ways:
// enabled clears the key from a CLONED engine map (the input map keeps it — nothing
// the caller holds is mutated), disabled leaves the engine map the input map itself,
// key intact.
func TestResolveInstanceSettingsFreeleech(t *testing.T) {
	t.Parallel()

	t.Run("on: engine map is a clone without the key", func(t *testing.T) {
		t.Parallel()
		raw := map[string]string{"freeleech": "true", "apikey": "k"}
		s, err := settingsResolver(time.Second).resolveInstanceSettings(
			context.Background(), domain.IndexerInstance{}, &loader.Definition{}, raw,
		)
		if err != nil {
			t.Fatalf("resolveInstanceSettings: %v", err)
		}
		if !s.Freeleech {
			t.Fatal("Freeleech = false, want true")
		}
		if _, ok := s.engineCfg["freeleech"]; ok {
			t.Error("engineCfg still carries the freeleech key; the engine must fetch the full catalog")
		}
		if s.engineCfg["apikey"] != "k" {
			t.Error("engineCfg lost an unrelated key in the clone")
		}
		if raw["freeleech"] != "true" {
			t.Error("the input map was mutated; the engine view must be a clone")
		}
	})

	t.Run("off (persisted literal false): key stays, flag off", func(t *testing.T) {
		t.Parallel()
		raw := map[string]string{"freeleech": "false"}
		s, err := settingsResolver(time.Second).resolveInstanceSettings(
			context.Background(), domain.IndexerInstance{}, &loader.Definition{}, raw,
		)
		if err != nil {
			t.Fatalf("resolveInstanceSettings: %v", err)
		}
		if s.Freeleech {
			t.Fatal("Freeleech = true for a persisted literal \"false\" (autobrr/harbrr#273)")
		}
		if _, ok := s.engineCfg["freeleech"]; !ok {
			t.Error("engineCfg dropped the key even though the view is off")
		}
	})
}

// TestResolveInstanceSettingsRefs pins the engineCfg-visibility decision: a
// referenced global proxy/solver is resolved INTO the engine's map (the same keys
// the inline settings use), because buildTransport and cardigann.SolverOption read
// those keys off the map — the typed struct carries the map, not parallel fields.
func TestResolveInstanceSettingsRefs(t *testing.T) {
	t.Parallel()
	reg, kr, db := newResolveRegistry(t)
	ctx := context.Background()

	proxyID := seedProxy(t, db, kr, domain.ProxyTypeSOCKS5, "10.0.0.9", 1080, "", "")
	solverID, err := (database.Solvers{}).InsertSolver(ctx, db, domain.Solver{
		Name: "fs", Type: domain.SolverTypeFlaresolverr, MaxTimeout: 120, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertSolver: %v", err)
	}
	senc, _ := kr.Encrypt(solverID, domain.SolverSecretURL, "http://flaresolverr:8191")
	if err := (database.Solvers{}).SetSolverSecret(ctx, db, solverID, senc, kr.KeyID()); err != nil {
		t.Fatalf("SetSolverSecret: %v", err)
	}

	inst := domain.IndexerInstance{ProxyID: &proxyID, SolverID: &solverID}
	s, err := reg.resolveInstanceSettings(ctx, inst, &loader.Definition{}, map[string]string{})
	if err != nil {
		t.Fatalf("resolveInstanceSettings: %v", err)
	}
	if s.engineCfg["proxy_type"] != domain.ProxyTypeSOCKS5 || s.engineCfg["proxy_url"] != "socks5://10.0.0.9:1080" {
		t.Errorf("proxy keys = %q %q; buildTransport reads them off the engine map", s.engineCfg["proxy_type"], s.engineCfg["proxy_url"])
	}
	if s.engineCfg["solver_type"] != domain.SolverTypeFlaresolverr ||
		s.engineCfg["flaresolverr_url"] != "http://flaresolverr:8191" ||
		s.engineCfg["flaresolverr_max_timeout"] != "120" {
		t.Errorf("solver keys = %q %q %q; SolverOption reads them off the engine map",
			s.engineCfg["solver_type"], s.engineCfg["flaresolverr_url"], s.engineCfg["flaresolverr_max_timeout"])
	}
}

// TestApplyWarmCapability covers the paging/Mode-consuming/eligible table: a driver
// warmOne would skip has its resolved WarmInterval zeroed (the setting can never do
// anything for it — no warmer ever refreshes its key); an eligible driver keeps it.
func TestApplyWarmCapability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		pages        bool
		consumesMode bool
		want         time.Duration
	}{
		{name: "paging driver zeroed", pages: true, want: 0},
		{name: "mode-consuming driver zeroed", consumesMode: true, want: 0},
		{name: "eligible driver kept", want: 15 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := instanceSettings{WarmInterval: 15 * time.Minute}
			s.applyWarmCapability(tt.pages, tt.consumesMode)
			if s.WarmInterval != tt.want {
				t.Fatalf("WarmInterval = %v, want %v", s.WarmInterval, tt.want)
			}
		})
	}
}

// TestSettingEnabled pins the fix for autobrr/harbrr#273: a checkbox-shaped setting
// persisted as the literal "false" (or any other value strconv.ParseBool recognizes as
// false) must read as OFF, not ON — the bare `!= ""` check it replaces treated any
// non-empty string, including "false", as checked.
func TestSettingEnabled(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty is off", "", false},
		{"false is off", "false", false},
		{"False is off", "False", false},
		{"FALSE is off", "FALSE", false},
		{"0 is off", "0", false},
		{"true is on", "true", true},
		{"True is on (cardigann's configTrue sentinel)", "True", true},
		{"1 is on", "1", true},
		{"yes is on (unparseable non-empty, permissive)", "yes", true},
		{"no is on (unparseable non-empty; ParseBool doesn't recognize \"no\" as false, so we deliberately stay permissive rather than guess)", "no", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := settingEnabled(tt.in); got != tt.want {
				t.Errorf("settingEnabled(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
