package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// BudgetCountersStore is the SQLite repository for the durable per-indexer
// request-budget counters (autobrr/harbrr#251): the query/grab counts for the
// current rolling period plus the reactive-learning "exhausted" latch per kind.
// Stateless: every method takes an Execer, mirroring IndexerStatCountersStore. No
// secret is ever stored here — only integer counts, period keys, and timestamps
// keyed by instance_id.
type BudgetCountersStore struct{}

// BudgetCounter is one instance's budget-counter row. Counts are absolute (not
// deltas); *_period is the period key (e.g. "2026-07-17" for a day, "2026-07-17T14"
// for an hour) the counts apply to — the registry compares it against the current
// period and resets on rollover rather than relying on a background sweep.
type BudgetCounter struct {
	InstanceID     int64
	QueryPeriod    string
	QueryCount     int64
	QueryExhausted bool
	GrabPeriod     string
	GrabCount      int64
	GrabExhausted  bool
	UpdatedAt      time.Time
}

// Get returns one instance's persisted budget counter, or found=false when no row
// exists yet (an instance never checked against a budget). The registry reads this
// once per instance, at first touch, and holds the result in memory thereafter.
func (BudgetCountersStore) Get(ctx context.Context, q dbinterface.Execer, instanceID int64) (BudgetCounter, bool, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT instance_id, query_period, query_count, query_exhausted,
			grab_period, grab_count, grab_exhausted, updated_at
			FROM indexer_budget_counters WHERE instance_id = ?`),
		instanceID)
	var (
		c              BudgetCounter
		queryExhausted int
		grabExhausted  int
		updatedAt      string
	)
	err := row.Scan(&c.InstanceID, &c.QueryPeriod, &c.QueryCount, &queryExhausted,
		&c.GrabPeriod, &c.GrabCount, &grabExhausted, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return BudgetCounter{}, false, nil
	}
	if err != nil {
		return BudgetCounter{}, false, fmt.Errorf("database: get budget counters for instance %d: %w", instanceID, err)
	}
	c.QueryExhausted = queryExhausted != 0
	c.GrabExhausted = grabExhausted != 0
	c.UpdatedAt = parseTime(updatedAt)
	return c, true, nil
}

// Upsert writes one instance's absolute counters, stamping updated_at. The DO UPDATE
// overwrites with the supplied values (excluded.*), so re-flushing the same totals is
// a no-op — there is no accumulation or reset here (the registry computes the
// rolled-over values before calling Upsert).
func (BudgetCountersStore) Upsert(ctx context.Context, q dbinterface.Execer, c BudgetCounter) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO indexer_budget_counters
			(instance_id, query_period, query_count, query_exhausted, grab_period, grab_count, grab_exhausted, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(instance_id) DO UPDATE SET
			  query_period = excluded.query_period,
			  query_count = excluded.query_count,
			  query_exhausted = excluded.query_exhausted,
			  grab_period = excluded.grab_period,
			  grab_count = excluded.grab_count,
			  grab_exhausted = excluded.grab_exhausted,
			  updated_at = excluded.updated_at`),
		c.InstanceID, c.QueryPeriod, c.QueryCount, boolToInt(c.QueryExhausted),
		c.GrabPeriod, c.GrabCount, boolToInt(c.GrabExhausted), c.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("database: upsert budget counters for instance %d: %w", c.InstanceID, err)
	}
	return nil
}
