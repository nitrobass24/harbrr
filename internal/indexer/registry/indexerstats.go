package registry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// IndexerStats holds the registry-wide, durable per-indexer query/grab/latency
// counters — the observability data the per-indexer stats surface reports. It mirrors
// the search-cache counter machinery (searchcache_counters.go) but is registry-owned
// because the instrumentation seam is the per-instance indexerAdapter, which the
// registry builds. There are NO cross-instance global totals here (each indexer's
// stats stand alone), so it is a simpler shape than the cache: no global atomics to
// keep in sync.
//
// The counters are process-durable: rehydrated from the store at boot, flushed back on
// a periodic tick and at shutdown. A hard crash between flushes loses the increments
// since the last flush — acceptable for observability-only counters, matching the same
// crash-loss tolerance the cache counters accept.
type IndexerStats struct {
	store database.IndexerStatCountersStore
	db    dbinterface.Querier
	clock func() time.Time
	log   zerolog.Logger

	// inst holds per-instance counters keyed by instanceID (map[int64]*instanceStat).
	inst sync.Map

	// rehydrated gates FlushCounters: it is set once RehydrateCounters has folded the
	// persisted counts at boot, so a failed/early flush can never overwrite the stored
	// absolute totals with this session's partial counts (mirrors
	// SearchCache.countersRehydrated).
	rehydrated atomic.Bool
}

// instanceStat holds one instance's cumulative query/grab counts, the running
// response-time sum (millis), and the last-query/last-grab times (unix millis, 0 =
// never). All lock-free atomics: the hot path (RecordQuery/RecordGrab) never touches
// the DB or a lock.
type instanceStat struct {
	queries       atomic.Int64
	grabs         atomic.Int64
	responseMs    atomic.Int64 // cumulative sum of per-query elapsed millis
	lastQueryUnix atomic.Int64 // unix millis, 0 = never
	lastGrabUnix  atomic.Int64 // unix millis, 0 = never
}

// newIndexerStats builds the counter set over db with the given clock and logger.
func newIndexerStats(db dbinterface.Querier, clock func() time.Time, log zerolog.Logger) *IndexerStats {
	if clock == nil {
		clock = time.Now
	}
	return &IndexerStats{db: db, clock: clock, log: log}
}

// get returns (creating on first use) the counter set for instanceID.
func (s *IndexerStats) get(instanceID int64) *instanceStat {
	v, _ := s.inst.LoadOrStore(instanceID, &instanceStat{})
	is, _ := v.(*instanceStat)
	return is
}

// RecordQuery counts one search that reached the tracker and folds in its elapsed
// latency. Lock-free hot path (no DB write): the increments are flushed durably on the
// periodic tick and at shutdown. A negative elapsed (a clock skew) is clamped to zero.
func (s *IndexerStats) RecordQuery(instanceID int64, elapsed time.Duration) {
	is := s.get(instanceID)
	is.queries.Add(1)
	ms := elapsed.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	is.responseMs.Add(ms)
	is.lastQueryUnix.Store(s.clock().UnixMilli())
}

// RecordGrab counts one successful grab (a download the /dl proxy actually produced).
func (s *IndexerStats) RecordGrab(instanceID int64) {
	is := s.get(instanceID)
	is.grabs.Add(1)
	is.lastGrabUnix.Store(s.clock().UnixMilli())
}

// snapshot reads instanceID's current counters (zeroes for an instance with no
// recorded traffic). The last-* unix-millis are converted to time.Time (zero = never).
func (s *IndexerStats) snapshot(instanceID int64) (queries, grabs, respTotal int64, lastQuery, lastGrab time.Time) {
	v, ok := s.inst.Load(instanceID)
	if !ok {
		return 0, 0, 0, time.Time{}, time.Time{}
	}
	is, _ := v.(*instanceStat)
	return is.queries.Load(), is.grabs.Load(), is.responseMs.Load(),
		unixMillisToTime(is.lastQueryUnix.Load()), unixMillisToTime(is.lastGrabUnix.Load())
}

// RehydrateCounters folds the persisted per-instance counters onto the in-memory
// atomics so the stats survive a restart instead of resetting to zero. Called once at
// boot; on success it sets rehydrated.
//
// The count adds are intentional (not Stores): at boot the atomics are zero, so adding
// the stored totals restores them exactly; on a self-heal retry after a failed boot
// load (see FlushCounters) the atomics already hold this session's own increments, so
// adding the restored totals yields the true sum. The rehydrated gate guarantees it
// takes effect at most once. The last-* times are set store-if-greater (max of stored
// vs current), so the same additive/idempotent invariant holds for them too.
func (s *IndexerStats) RehydrateCounters(ctx context.Context) error {
	rows, err := s.store.AllCounters(ctx, s.db)
	if err != nil {
		return fmt.Errorf("registry: load indexer stat counters: %w", err)
	}
	for _, r := range rows {
		is := s.get(r.InstanceID)
		is.queries.Add(r.Queries)
		is.grabs.Add(r.Grabs)
		is.responseMs.Add(r.ResponseMsTotal)
		storeIfGreater(&is.lastQueryUnix, timeToUnixMillis(r.LastQueryAt))
		storeIfGreater(&is.lastGrabUnix, timeToUnixMillis(r.LastGrabAt))
	}
	s.rehydrated.Store(true)
	return nil
}

// FlushCounters writes the live per-instance counters to the store so they survive a
// restart. It writes ABSOLUTE cumulative values (the atomics already hold the
// rehydrated total), so the UPSERT is idempotent. Best-effort PER ROW: a failure is
// logged (instance id + redacted error) and the next instance still flushes, so a
// just-deleted instance's cascade-removed row (FK error on re-insert) never aborts the
// rest.
//
// If a boot RehydrateCounters never succeeded, this retries it first: until a load
// succeeds it must NOT flush, or an absolute write would clobber the stored totals with
// this session's partial counts. RehydrateCounters is additive and gated, so the retry
// folds the session's own increments onto the restored totals exactly once.
func (s *IndexerStats) FlushCounters(ctx context.Context) {
	if !s.rehydrated.Load() {
		if err := s.RehydrateCounters(ctx); err != nil {
			s.log.Warn().Str("error", apphttp.RedactError(err)).
				Msg("registry: indexer stat counter rehydrate retry failed; skipping flush")
			return
		}
	}
	now := s.clock()
	s.inst.Range(func(k, v any) bool {
		id, _ := k.(int64)
		is, _ := v.(*instanceStat)
		row := database.IndexerStatCounter{
			InstanceID:      id,
			Queries:         is.queries.Load(),
			Grabs:           is.grabs.Load(),
			ResponseMsTotal: is.responseMs.Load(),
			LastQueryAt:     unixMillisToTime(is.lastQueryUnix.Load()),
			LastGrabAt:      unixMillisToTime(is.lastGrabUnix.Load()),
			UpdatedAt:       now,
		}
		if err := s.store.Upsert(ctx, s.db, row); err != nil {
			s.log.Warn().Int64("instance_id", id).Str("error", apphttp.RedactError(err)).
				Msg("registry: indexer stat counter flush failed")
		}
		return true
	})
}

// ForgetInstance drops a deleted instance's in-memory counters. Unlike the cache there
// are no global totals to decrement, so it is a bare delete; the durable
// indexer_stat_counters row is already gone via ON DELETE CASCADE.
func (s *IndexerStats) ForgetInstance(instanceID int64) {
	s.inst.LoadAndDelete(instanceID)
}

// storeIfGreater sets a to v only when v is larger, so folding a stored last-* time
// onto a session that already advanced past it never rewinds the timestamp. It uses a
// CAS loop so the read-modify-write is atomic: a concurrent RecordQuery advancing the
// same timestamp during the FlushCounters self-heal retry can never be lost.
func storeIfGreater(a *atomic.Int64, v int64) {
	for {
		cur := a.Load()
		if v <= cur || a.CompareAndSwap(cur, v) {
			return
		}
	}
}

// unixMillisToTime converts stored unix millis to a UTC time.Time; 0 means never (the
// zero time).
func unixMillisToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// timeToUnixMillis converts a time.Time to unix millis; the zero time maps to 0
// (never).
func timeToUnixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
