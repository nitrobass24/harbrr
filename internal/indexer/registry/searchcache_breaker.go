package registry

import (
	"errors"
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// negativeBreaker is the per-instance circuit breaker that spares a failing tracker
// from being re-hit by every consumer. When a live search to an instance fails with
// a transient/overload error (rate-limit, timeout, an unreachable tracker), the
// breaker trips for that instance for a short window; while open, a cache MISS for
// that instance short-circuits to the recorded error instead of driving the tracker
// again. A still-fresh positive cache entry is unaffected — only misses consult it,
// so an open breaker never blanks out cached results.
//
// SECRETS: the stored error is the same typed engine error the live path already
// returns (e.g. *search.RateLimitedError, or a transport error already redacted by
// the paced client). The breaker carries no URL/passkey of its own, never logs the
// error, and only replays it down the path that would have produced it live.
type negativeBreaker struct {
	mu      sync.Mutex
	entries map[int64]breakerEntry
}

// breakerEntry is one instance's open window and the error to replay while open.
type breakerEntry struct {
	until time.Time
	err   error
}

func newNegativeBreaker() *negativeBreaker {
	return &negativeBreaker{entries: make(map[int64]breakerEntry)}
}

// replay returns the recorded error when the breaker is open for instanceID at now,
// or nil when it is closed. A lapsed entry is treated as closed and dropped lazily so
// the next caller probes the tracker live (a natural half-open: the first request
// after the window goes through).
func (b *negativeBreaker) replay(instanceID int64, now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[instanceID]
	if !ok {
		return nil
	}
	if !now.Before(e.until) {
		delete(b.entries, instanceID)
		return nil
	}
	return e.err
}

// trip opens the breaker for instanceID until `until`, recording err for replay.
func (b *negativeBreaker) trip(instanceID int64, until time.Time, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[instanceID] = breakerEntry{until: until, err: err}
}

// classifyBreakerError decides whether a live search failure should trip the breaker
// and until when. With the breaker armed (negativeTTL > 0) any live error trips it:
// at the cache layer a non-nil Search error means the tracker did not return usable
// results, so re-driving it for every other consumer only pesters a struggling
// tracker. A rate-limit response extends the window to its Retry-After when that is
// longer than negativeTTL (honor the tracker's explicit ask). A caller-cancelled
// context is filtered out earlier by tripBreaker, not here.
func classifyBreakerError(err error, negativeTTL time.Duration, now time.Time) (time.Time, bool) {
	if err == nil || negativeTTL <= 0 {
		return time.Time{}, false
	}
	window := negativeTTL
	var rle *search.RateLimitedError
	if errors.As(err, &rle) && rle.RetryAfter > window {
		window = rle.RetryAfter
	}
	return now.Add(window), true
}
