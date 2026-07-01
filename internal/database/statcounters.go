package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// IndexerStatCountersStore is the SQLite repository for the durable per-instance
// query/grab/latency counters. Stateless: every method takes an Execer, so callers
// pass *DB or a transaction handle, mirroring CacheCountersStore. No secret is ever
// stored here — only integer counts and timestamps keyed by instance_id.
type IndexerStatCountersStore struct{}

// IndexerStatCounter is one instance's cumulative query/grab counters plus the
// running response-time total (avg is derived at read time). The counts are absolute
// (not deltas): the registry rehydrates them into its in-memory atomics at boot and
// writes the live absolute values back on flush, so the Upsert is idempotent.
// LastQueryAt/LastGrabAt are zero when the indexer has never been searched/grabbed.
type IndexerStatCounter struct {
	InstanceID      int64
	Queries         int64
	Grabs           int64
	ResponseMsTotal int64
	LastQueryAt     time.Time // zero = never
	LastGrabAt      time.Time // zero = never
	UpdatedAt       time.Time
}

// Upsert writes one instance's absolute counters, stamping updated_at. The DO UPDATE
// overwrites with the supplied values (excluded.*), so re-flushing the same totals is
// a no-op — there is no accumulation or reset. The two last-* timestamps store NULL
// when zero (never observed). The caller supplies the time (the store stays clock-free
// for testability).
func (IndexerStatCountersStore) Upsert(ctx context.Context, q dbinterface.Execer, c IndexerStatCounter) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO indexer_stat_counters
			(instance_id, queries, grabs, response_ms_total, last_query_at, last_grab_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(instance_id) DO UPDATE SET
			  queries = excluded.queries,
			  grabs = excluded.grabs,
			  response_ms_total = excluded.response_ms_total,
			  last_query_at = excluded.last_query_at,
			  last_grab_at = excluded.last_grab_at,
			  updated_at = excluded.updated_at`),
		c.InstanceID, c.Queries, c.Grabs, c.ResponseMsTotal,
		nullableTime(c.LastQueryAt), nullableTime(c.LastGrabAt), c.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("database: upsert indexer stat counters for instance %d: %w", c.InstanceID, err)
	}
	return nil
}

// AllCounters returns every persisted counter row, ordered by instance_id. The
// registry loads these into its in-memory atomics at boot.
func (IndexerStatCountersStore) AllCounters(ctx context.Context, q dbinterface.Execer) ([]IndexerStatCounter, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT instance_id, queries, grabs, response_ms_total, last_query_at, last_grab_at, updated_at
			FROM indexer_stat_counters ORDER BY instance_id`)
	if err != nil {
		return nil, fmt.Errorf("database: list indexer stat counters: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []IndexerStatCounter
	for rows.Next() {
		var (
			c                   IndexerStatCounter
			lastQuery, lastGrab sql.NullString
			updatedAt           string
		)
		if err := rows.Scan(&c.InstanceID, &c.Queries, &c.Grabs, &c.ResponseMsTotal,
			&lastQuery, &lastGrab, &updatedAt); err != nil {
			return nil, fmt.Errorf("database: scan indexer stat counters: %w", err)
		}
		if lastQuery.Valid {
			c.LastQueryAt = parseTime(lastQuery.String)
		}
		if lastGrab.Valid {
			c.LastGrabAt = parseTime(lastGrab.String)
		}
		c.UpdatedAt = parseTime(updatedAt)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate indexer stat counters: %w", err)
	}
	return out, nil
}

// nullableTime maps a zero time to a NULL column value (never observed) and a set
// time to its RFC3339 UTC string, so "never" stays distinct from a real timestamp.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(timeLayout)
}
