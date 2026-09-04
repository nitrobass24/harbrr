package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// rateDefaultSetting is the persisted global rate-limit default (autobrr/harbrr#104) —
// the process-wide preference RateDefault applies for any indexer with no
// "rate_interval" override, never below the definition's own requestDelay (see
// resolveRateInterval). A DB row overrides the hardcoded defaultRateInterval seed; a
// missing, malformed or non-positive row reads back as that seed.
var rateDefaultSetting = database.Setting[time.Duration]{
	Key:     "rate.default_interval",
	Default: defaultRateInterval,
	Parse:   parsePositiveDuration,
	Format:  time.Duration.String,
}

// parsePositiveDuration rejects a non-positive interval, which would disable pacing
// entirely rather than slow it down.
func parsePositiveDuration(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	switch {
	case err != nil:
		return 0, fmt.Errorf("parse rate interval: %w", err)
	case d <= 0:
		return 0, fmt.Errorf("interval %s must be positive", d)
	}
	return d, nil
}

// RateDefault reads the live global rate-limit default. Lock-free: it's an
// atomic.Int64 seeded in New() and swapped by SetRateDefault, so every buildAdapter
// call sees the current value without contending with a concurrent write.
func (r *Resolver) RateDefault() time.Duration {
	return time.Duration(r.rateDefault.Load())
}

// SetRateDefault parses, validates, persists, and applies a new global rate-limit
// default (a Go duration string, e.g. "2s"). It persists BEFORE swapping the live
// value, so a failed write never desyncs runtime and stored state, then calls
// InvalidateAll — the same "flush every cached engine" mechanism proxy/solver
// resource edits already use — so the new default reaches every indexer's paced
// client on its next resolve, live, without a restart. rateMu serializes this
// against a concurrent SetRateDefault.
func (r *Resolver) SetRateDefault(ctx context.Context, interval string) error {
	d, err := parsePositiveDuration(interval)
	if err != nil {
		return fmt.Errorf("%w: rate default interval must be a positive Go duration, e.g. 1s", ErrInvalid)
	}
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	if err := rateDefaultSetting.Write(ctx, r.db, d, r.clock()); err != nil {
		return fmt.Errorf("registry: persist rate default: %w", err)
	}
	r.rateDefault.Store(int64(d))
	r.InvalidateAll()
	return nil
}

// LoadRateDefaultOverride overlays the persisted override onto the hardcoded seed.
// Called once at boot; a missing or unusable row leaves the seed in place, so
// operator config can never brick startup.
func (r *Resolver) LoadRateDefaultOverride(ctx context.Context) {
	r.rateDefault.Store(int64(rateDefaultSetting.Read(ctx, r.db, r.log)))
}
