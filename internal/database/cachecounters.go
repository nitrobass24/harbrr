package database

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// CacheCountersStore is the SQLite repository for the durable per-instance
// search-cache hit/miss/suppressed counters. Stateless: every method takes an
// Execer, so callers pass *DB or a transaction handle, mirroring SearchCacheStore
// and AppSettings. No secret is ever stored here — only integer counts keyed by
// instance_id.
type CacheCountersStore struct{}

// CacheCounter is one instance's cumulative cache counters. The counts are absolute
// (not deltas): the registry rehydrates them into its in-memory atomics at boot and
// writes the live absolute values back on flush, so the Upsert is idempotent.
type CacheCounter struct {
	InstanceID int64
	Hits       int64
	Misses     int64
	Suppressed int64
	UpdatedAt  time.Time
}

// Upsert writes one instance's absolute counters, stamping updated_at. The DO UPDATE
// overwrites with the supplied values (excluded.*), so re-flushing the same totals is
// a no-op — there is no accumulation or reset. The caller supplies the time (the store
// stays clock-free for testability).
func (CacheCountersStore) Upsert(ctx context.Context, q dbinterface.Execer, c CacheCounter) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO cache_counters (instance_id, hits, misses, suppressed, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(instance_id) DO UPDATE SET
			  hits = excluded.hits,
			  misses = excluded.misses,
			  suppressed = excluded.suppressed,
			  updated_at = excluded.updated_at`),
		c.InstanceID, c.Hits, c.Misses, c.Suppressed, c.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("database: upsert cache counters for instance %d: %w", c.InstanceID, err)
	}
	return nil
}

// AllCounters returns every persisted counter row, ordered by instance_id. The
// registry loads these into its in-memory atomics at boot.
func (CacheCountersStore) AllCounters(ctx context.Context, q dbinterface.Execer) ([]CacheCounter, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT instance_id, hits, misses, suppressed, updated_at
			FROM cache_counters ORDER BY instance_id`)
	if err != nil {
		return nil, fmt.Errorf("database: list cache counters: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []CacheCounter
	for rows.Next() {
		var (
			c         CacheCounter
			updatedAt string
		)
		if err := rows.Scan(&c.InstanceID, &c.Hits, &c.Misses, &c.Suppressed, &updatedAt); err != nil {
			return nil, fmt.Errorf("database: scan cache counters: %w", err)
		}
		c.UpdatedAt = parseTime(updatedAt)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate cache counters: %w", err)
	}
	return out, nil
}
