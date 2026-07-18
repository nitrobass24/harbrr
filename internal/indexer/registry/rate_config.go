package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// keyRateDefaultInterval is the app_settings key for the live global rate-limit
// default (autobrr/harbrr#104) — the process-wide preference RateDefault applies for
// any indexer with no "rate_interval" override, never below the definition's own
// requestDelay (see resolveRateInterval). Mirrors the cache-config / log-level "DB
// row overrides the seed" model; the seed here is the hardcoded defaultRateInterval.
const keyRateDefaultInterval = "rate.default_interval"

// RateDefault reads the live global rate-limit default. Lock-free: it's an
// atomic.Int64 seeded in New() and swapped by SetRateDefault, so every buildAdapter
// call sees the current value without contending with a concurrent write.
func (r *Resolver) RateDefault() time.Duration {
	return time.Duration(r.rateDefault.Load())
}

// SetRateDefault parses, validates, persists, and applies a new global rate-limit
// default (a Go duration string, e.g. "2s"). It persists BEFORE swapping the live
// value (mirrors LogLevelStore.Set) so a failed write never desyncs runtime and
// stored state, then calls InvalidateAll — the same "flush every cached engine"
// mechanism proxy/solver resource edits already use — so the new default reaches
// every indexer's paced client on its next resolve, live, without a restart.
// rateMu serializes this against a concurrent SetRateDefault.
func (r *Resolver) SetRateDefault(ctx context.Context, interval string) error {
	d, err := time.ParseDuration(interval)
	if err != nil || d <= 0 {
		return fmt.Errorf("%w: rate default interval must be a positive Go duration, e.g. 1s", ErrInvalid)
	}
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	if err := (database.AppSettings{}).Set(ctx, r.db, keyRateDefaultInterval, d.String(), r.clock()); err != nil {
		return fmt.Errorf("registry: persist rate default: %w", err)
	}
	r.rateDefault.Store(int64(d))
	r.InvalidateAll()
	return nil
}

// LoadRateDefaultOverride overlays a persisted app_settings override (if present and
// valid) onto the hardcoded seed. Called once at boot, mirroring
// SearchCache.LoadOverrides / LogLevelStore.ApplyPersisted. A missing, malformed, or
// non-positive stored value is ignored (the seed stands) — operator config must
// never brick startup.
func (r *Resolver) LoadRateDefaultOverride(ctx context.Context) error {
	stored, found, err := (database.AppSettings{}).Get(ctx, r.db, keyRateDefaultInterval)
	if err != nil {
		return fmt.Errorf("registry: load rate default: %w", err)
	}
	if !found {
		return nil
	}
	d, err := time.ParseDuration(stored)
	if err != nil || d <= 0 {
		return nil
	}
	r.rateDefault.Store(int64(d))
	return nil
}
