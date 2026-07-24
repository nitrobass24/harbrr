package registry

import (
	"sync"
	"time"
)

// windowHours is the rolling window the 24h stats view covers.
const windowHours = 24

// hourWindow accumulates hit/miss counts into per-hour buckets so the stats
// surface can report a rolling 24h view next to the lifetime counters. Global
// only (the dashboard tile) — no per-instance breakdown.
// ponytail: in-memory only, so the 24h view restarts empty after a reboot;
// persist buckets alongside counterStore if that ever matters.
type hourWindow struct {
	mu sync.Mutex
	// buckets is a ring keyed by unix-hour modulo len; stamps records which
	// unix-hour each slot currently holds so a stale slot is zeroed on reuse
	// (one extra slot so the partial current hour never evicts the 24th).
	buckets [windowHours + 1]struct{ hits, misses int64 }
	stamps  [windowHours + 1]int64
}

// hit records one cache hit in the current hour's bucket.
func (w *hourWindow) hit(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.slot(now).hits++
}

// miss records one successfully-resolved cache miss in the current hour's bucket.
func (w *hourWindow) miss(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.slot(now).misses++
}

// slot returns the current hour's bucket, zeroing it first if it still holds an
// old hour. Caller must hold w.mu.
func (w *hourWindow) slot(now time.Time) *struct{ hits, misses int64 } {
	h := now.Unix() / 3600
	i := int(h % int64(len(w.buckets)))
	if w.stamps[i] != h {
		w.stamps[i] = h
		w.buckets[i] = struct{ hits, misses int64 }{}
	}
	return &w.buckets[i]
}

// totals sums the buckets stamped within the last windowHours hours.
func (w *hourWindow) totals(now time.Time) (hits, misses int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	oldest := now.Unix()/3600 - windowHours + 1
	for i, stamp := range w.stamps {
		if stamp >= oldest {
			hits += w.buckets[i].hits
			misses += w.buckets[i].misses
		}
	}
	return hits, misses
}

// reset zeroes the window (cache flush resets the stats).
func (w *hourWindow) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buckets = [windowHours + 1]struct{ hits, misses int64 }{}
	w.stamps = [windowHours + 1]int64{}
}
