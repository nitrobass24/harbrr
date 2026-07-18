package registry

import (
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// defWithDelay builds a definition declaring requestDelay seconds (nil when
// seconds <= 0, mirroring a def that declares none).
func defWithDelay(seconds float64) *loader.Definition {
	if seconds <= 0 {
		return &loader.Definition{}
	}
	return &loader.Definition{RequestDelay: &seconds}
}

// TestResolveRateInterval pins the autobrr/harbrr#104 formula: the per-indexer
// override REPLACES the global default (not max()'d against it) — an operator can
// set one indexer faster than the global default — but the definition's own
// requestDelay is always a floor neither can undercut.
func TestResolveRateInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		def           *loader.Definition
		cfg           map[string]string
		globalDefault time.Duration
		want          time.Duration
	}{
		{
			name:          "no def delay, no override: global default wins",
			def:           defWithDelay(0),
			cfg:           nil,
			globalDefault: time.Second,
			want:          time.Second,
		},
		{
			name:          "def delay above global default: def floor wins",
			def:           defWithDelay(3),
			cfg:           nil,
			globalDefault: time.Second,
			want:          3 * time.Second,
		},
		{
			name:          "def delay below global default: global default wins",
			def:           defWithDelay(1),
			cfg:           nil,
			globalDefault: 5 * time.Second,
			want:          5 * time.Second,
		},
		{
			name:          "override above global default: override wins",
			def:           defWithDelay(0),
			cfg:           map[string]string{"rate_interval": "10s"},
			globalDefault: time.Second,
			want:          10 * time.Second,
		},
		{
			// The coordinator-called-out case: override REPLACES the global default
			// in the loosening direction too, as long as it stays >= the def floor —
			// a three-way max() would wrongly ignore this override.
			name:          "override below global default but above def floor: override wins",
			def:           defWithDelay(1),
			cfg:           map[string]string{"rate_interval": "2s"},
			globalDefault: 5 * time.Second,
			want:          2 * time.Second,
		},
		{
			name:          "override below the def floor: def floor wins, never undercut",
			def:           defWithDelay(5),
			cfg:           map[string]string{"rate_interval": "500ms"},
			globalDefault: time.Second,
			want:          5 * time.Second,
		},
		{
			name:          "empty override is ignored: global default wins",
			def:           defWithDelay(0),
			cfg:           map[string]string{"rate_interval": ""},
			globalDefault: 2 * time.Second,
			want:          2 * time.Second,
		},
		{
			name:          "malformed override is ignored: global default wins",
			def:           defWithDelay(0),
			cfg:           map[string]string{"rate_interval": "not-a-duration"},
			globalDefault: 2 * time.Second,
			want:          2 * time.Second,
		},
		{
			name:          "non-positive override is ignored: global default wins",
			def:           defWithDelay(0),
			cfg:           map[string]string{"rate_interval": "0s"},
			globalDefault: 2 * time.Second,
			want:          2 * time.Second,
		},
		{
			name:          "nil def is treated as no floor",
			def:           nil,
			cfg:           nil,
			globalDefault: 2 * time.Second,
			want:          2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveRateInterval(tt.def, tt.cfg, tt.globalDefault); got != tt.want {
				t.Errorf("resolveRateInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefRequestDelay(t *testing.T) {
	t.Parallel()
	if got := defRequestDelay(nil); got != 0 {
		t.Errorf("defRequestDelay(nil) = %v, want 0", got)
	}
	if got := defRequestDelay(defWithDelay(0)); got != 0 {
		t.Errorf("defRequestDelay(unset) = %v, want 0", got)
	}
	if got := defRequestDelay(defWithDelay(2.5)); got != 2500*time.Millisecond {
		t.Errorf("defRequestDelay(2.5) = %v, want 2.5s", got)
	}
}
