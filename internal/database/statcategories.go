package database

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// IndexerCategoryStatsStore is the SQLite repository for the per-indexer,
// per-parent-category, per-month tallies (autobrr/harbrr#403). Stateless: every method
// takes an Execer, like IndexerStatCountersStore. No secret is ever stored here — only
// counts keyed by instance id, category id and month bucket.
//
// The write is an ADDITIVE upsert of a delta, not an absolute write: the registry keeps
// only the increments since its last flush in memory, so nothing has to be rehydrated
// and no history is ever loaded to aggregate (the OOM constraint in #403).
type IndexerCategoryStatsStore struct{}

// BucketLayout is the month-bucket format ("2006-01", UTC). Lexical order equals
// chronological order, so retention is a plain `bucket < ?` range delete.
const BucketLayout = "2006-01"

// MonthBucket is the bucket t falls in (UTC).
func MonthBucket(t time.Time) string { return t.UTC().Format(BucketLayout) }

// IndexerCategoryCount is one (instance, parent category) pair's result/grab counts —
// the increments since the last flush on the way in, the retained totals on the way
// out. CategoryID is a standard PARENT category id (2000, 5000, …) or 0 when the
// release carried no mappable standard category.
type IndexerCategoryCount struct {
	InstanceID   int64
	CategoryID   int
	QueryResults int64
	Grabs        int64
}

// AddDeltas folds the increments into bucket, creating the rows on first use. Each row
// is applied independently (best-effort per row, like the counter flush): a
// just-deleted instance's FK failure never aborts the rest. It returns the deltas that
// did NOT land plus the first error, so the caller can retry exactly those (an
// increment that was written must never be replayed).
func (IndexerCategoryStatsStore) AddDeltas(ctx context.Context, q dbinterface.Execer, bucket string, deltas []IndexerCategoryCount, now time.Time) ([]IndexerCategoryCount, error) {
	stamp := now.UTC().Format(timeLayout)
	var (
		firstErr error
		failed   []IndexerCategoryCount
	)
	for _, d := range deltas {
		_, err := q.ExecContext(ctx,
			q.Rebind(`INSERT INTO indexer_category_stats
				(instance_id, category_id, bucket, query_results, grabs, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(instance_id, category_id, bucket) DO UPDATE SET
				  query_results = query_results + excluded.query_results,
				  grabs = grabs + excluded.grabs,
				  updated_at = excluded.updated_at`),
			d.InstanceID, d.CategoryID, bucket, d.QueryResults, d.Grabs, stamp)
		if err != nil {
			// A foreign-key violation is TERMINAL, not transient: the instance was
			// deleted between accumulation and this flush, so its tallies can never
			// land. Returning it for retry would re-fail on every flush tick forever;
			// discard it instead (and raise no error — losing a deleted instance's
			// counts is the intended outcome, not an incident).
			if IsForeignKeyViolation(err) {
				continue
			}
			failed = append(failed, d)
			if firstErr == nil {
				firstErr = fmt.Errorf("database: add indexer category stats for instance %d: %w", d.InstanceID, err)
			}
		}
	}
	return failed, firstErr
}

// Tallies returns every instance's per-category totals in ONE grouped query, summed
// over the retained buckets (retention has already deleted anything older, so no time
// filter is needed here). Cardinality is instances × ~10 parent categories, so the
// all-instances read is cheaper than a per-indexer query per row of the stats list.
func (IndexerCategoryStatsStore) Tallies(ctx context.Context, q dbinterface.Execer) ([]IndexerCategoryCount, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT instance_id, category_id, SUM(query_results), SUM(grabs)
			FROM indexer_category_stats
			GROUP BY instance_id, category_id
			ORDER BY instance_id, category_id`)
	if err != nil {
		return nil, fmt.Errorf("database: list indexer category stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []IndexerCategoryCount
	for rows.Next() {
		var t IndexerCategoryCount
		if err := rows.Scan(&t.InstanceID, &t.CategoryID, &t.QueryResults, &t.Grabs); err != nil {
			return nil, fmt.Errorf("database: scan indexer category stats: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate indexer category stats: %w", err)
	}
	return out, nil
}

// DeleteBefore drops every bucket older than the given month ("2006-01"), the retention
// reap. Lexical comparison is chronological for the bucket layout.
func (IndexerCategoryStatsStore) DeleteBefore(ctx context.Context, q dbinterface.Execer, bucket string) (int64, error) {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM indexer_category_stats WHERE bucket < ?`), bucket)
	if err != nil {
		return 0, fmt.Errorf("database: delete indexer category stats before %q: %w", bucket, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("database: delete indexer category stats before %q: rows affected: %w", bucket, err)
	}
	return n, nil
}

// Category-stat retention bounds. A year of monthly buckets is the renewal-decision
// window #403 asks for; the ceiling keeps a typo from making retention effectively
// infinite.
const (
	DefaultCategoryStatsRetentionMonths = 12
	MinCategoryStatsRetentionMonths     = 1
	MaxCategoryStatsRetentionMonths     = 120
)

// CategoryStatsRetention is the operator's retention window for the per-category
// tallies, in months. A missing or out-of-bounds row reads back as the default, so a
// stale row can never wedge the reaper.
var CategoryStatsRetention = Setting[int]{
	Key:     "stats_category_retention_months",
	Default: DefaultCategoryStatsRetentionMonths,
	Parse:   parseRetentionMonths,
	Format:  strconv.Itoa,
}

// parseRetentionMonths rejects anything outside the accepted window, which is what
// makes an out-of-range stored value fall back to the default rather than take effect.
func parseRetentionMonths(raw string) (int, error) {
	months, err := strconv.Atoi(raw)
	switch {
	case err != nil:
		return 0, fmt.Errorf("parse retention months: %w", err)
	case months < MinCategoryStatsRetentionMonths || months > MaxCategoryStatsRetentionMonths:
		return 0, fmt.Errorf("retention of %d months is outside %d-%d",
			months, MinCategoryStatsRetentionMonths, MaxCategoryStatsRetentionMonths)
	}
	return months, nil
}
