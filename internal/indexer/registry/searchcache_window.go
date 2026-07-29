package registry

import (
	"sync"
	"time"
)

// The rolling windows the stats surface offers, in hours. One accumulator serves
// them all: a shorter view is a suffix sum over the same ring, so the ring is sized
// to the WIDEST window (30d at hour granularity = 720 buckets + the boundary slot,
// ~11.5 KB — cheaper than keeping three accumulators in step).
const (
	dayHours       = 24
	weekHours      = 7 * 24
	monthHours     = 30 * 24
	maxWindowHours = monthHours
)

// hourWindow accumulates hit/miss counts into per-hour buckets so the stats surface
// can report rolling views next to the lifetime counters. Global only (the dashboard
// tile) — no per-instance breakdown.
// ponytail: in-memory only, so the rolling views restart empty after a reboot;
// `since` reports how far back they actually reach so the surface never implies a
// month of data it does not have. Persist buckets alongside counterStore if that
// ever stops being good enough.
type hourWindow struct {
	mu sync.Mutex
	// buckets is a ring keyed by unix-hour modulo len; stamps records which
	// unix-hour each slot currently holds so a stale slot is zeroed on reuse
	// (one extra slot so the partial current hour never evicts the oldest).
	buckets [maxWindowHours + 1]struct{ hits, misses int64 }
	stamps  [maxWindowHours + 1]int64
	// since is when this ring started accumulating — process start, or the last
	// reset. It bounds every bucketed view's real coverage.
	since time.Time
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

// totals sums the buckets covering the trailing `hours` hours. The oldest bucket
// overlaps the window boundary only partially, but it is INCLUDED: at hour
// granularity the view must never undercount a mid-hour event that is still inside
// the trailing window, so it spans up to one extra partial hour instead (the ring's
// +1 slot exists precisely so this boundary bucket is still resident at the widest
// window, and every narrower window is a suffix of the same ring).
func (w *hourWindow) totals(now time.Time, hours int) (hits, misses int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	oldest := now.Unix()/3600 - int64(hours)
	for i, stamp := range w.stamps {
		if stamp >= oldest {
			hits += w.buckets[i].hits
			misses += w.buckets[i].misses
		}
	}
	return hits, misses
}

// coverageSince reports when the ring started accumulating, so a caller can tell a
// 30d view backed by an hour of uptime apart from one backed by a month.
func (w *hourWindow) coverageSince() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.since
}

// reset zeroes the window and restarts its coverage clock at now (a stats reset, and
// the cache's own construction).
func (w *hourWindow) reset(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buckets = [maxWindowHours + 1]struct{ hits, misses int64 }{}
	w.stamps = [maxWindowHours + 1]int64{}
	w.since = now
}
