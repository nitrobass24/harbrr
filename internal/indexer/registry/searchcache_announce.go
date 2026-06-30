package registry

import (
	"context"
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// announceDedupWindow is how long a just-announced GUID is suppressed from re-announcing.
// It guards the gap where a cache entry expires and refills: the prior-entry diff would
// otherwise see the same releases as "new" again.
const announceDedupWindow = 6 * time.Hour

// announceDedupMax bounds the in-memory dedup set so a busy instance can't grow it without
// limit; the oldest entries are pruned past this size.
const announceDedupMax = 5000

// AnnounceSink receives the releases a cache write-back newly observed for an instance (an
// RSS/empty-query fill only). It is the seam the announce service hooks: the registry stays
// unaware of cross-seed, and the sink is nil (a no-op) when no announce targets exist.
// fresh is never mutated by the sink (it shares the cached slice).
type AnnounceSink func(ctx context.Context, instanceID int64, fresh []*normalizer.Release)

// tapAnnounce derives the "what's new" stream from a cache write-back: it diffs the fresh
// releases' GUIDs against the prior cached entry (the one this write overwrites) and a
// short dedup window, and hands any genuinely-new releases to the sink. It is gated to
// empty/RSS queries (isEmptyQuery) so it announces only what a consumer already polls —
// zero added tracker load. Best-effort: a prior-entry read failure simply treats every
// fresh release as new (the dedup window still prevents a storm).
func (c *SearchCache) tapAnnounce(ctx context.Context, instanceID int64, q search.Query, key string, fresh []*normalizer.Release) {
	if c.announceSink == nil || !isEmptyQuery(q) || len(fresh) == 0 {
		return
	}
	prior := c.priorGUIDs(ctx, key)
	now := c.clock()
	out := make([]*normalizer.Release, 0, len(fresh))
	for _, r := range fresh {
		guid := tzn.GUIDFor(r)
		if _, seen := prior[guid]; seen {
			continue
		}
		// seenAndMark is one atomic check-and-record, so two concurrent taps for the same
		// GUID (a request miss racing the SWR refresh on the same key) announce it once.
		if c.announced.seenAndMark(guid, now) {
			continue
		}
		out = append(out, r)
	}
	if len(out) > 0 {
		c.announceSink(ctx, instanceID, out)
	}
}

// priorGUIDs returns the GUID set of the cache entry currently stored under key (the one a
// write-back is about to overwrite), or an empty set when there is none or it can't be
// read. It uses FetchAny (ignoring expiry): on the request miss path the entry being
// overwritten is by definition expired, and the on-disk payload survives a restart, so
// this is what makes the diff suppress already-seen releases on a miss and across restarts.
// The payload is never logged (it carries passkey-bearing links).
func (c *SearchCache) priorGUIDs(ctx context.Context, key string) map[string]struct{} {
	set := map[string]struct{}{}
	entry, found, err := c.store.FetchAny(ctx, c.db, key)
	if err != nil || !found {
		return set
	}
	prior, err := decodeReleases(entry.ResultsJSON, key)
	if err != nil {
		return set
	}
	for _, r := range prior {
		set[tzn.GUIDFor(r)] = struct{}{}
	}
	return set
}

// announceWindow is a bounded, time-windowed set of recently-announced GUIDs. It is the
// dedup guard across cache expiry/refill so a release announces at most once per window.
type announceWindow struct {
	mu     sync.Mutex
	seenAt map[string]time.Time
}

func newAnnounceWindow() *announceWindow {
	return &announceWindow{seenAt: map[string]time.Time{}}
}

// seenAndMark atomically reports whether guid was announced within the dedup window and,
// when it was not, records it as announced now. The check and the record are one critical
// section so concurrent taps for the same GUID (a request miss racing the SWR refresh)
// can never both treat it as new. It prunes window-expired entries when the map grows past
// the size cap (the cap bounds steady-state memory; a burst of >announceDedupMax distinct
// GUIDs within one window is held until those entries age out).
func (w *announceWindow) seenAndMark(guid string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if at, ok := w.seenAt[guid]; ok && now.Sub(at) < announceDedupWindow {
		return true
	}
	w.seenAt[guid] = now
	if len(w.seenAt) > announceDedupMax {
		for g, at := range w.seenAt {
			if now.Sub(at) >= announceDedupWindow {
				delete(w.seenAt, g)
			}
		}
	}
	return false
}
