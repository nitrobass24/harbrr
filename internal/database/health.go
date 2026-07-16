package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Health is the SQLite repository for the append-only indexer_health_events table.
// Stateless: every method takes an Execer (so it runs standalone or in a tx) and
// routes every placeholder through q.Rebind. Mirrors AppMeta/Instances.
type Health struct{}

// HealthRecovery is the latest successful explicit indexer test. ThroughEventID is
// the newest failure present when the test passed; OccurredAt is a fallback ordering
// signal for the case where retention empties the event table and SQLite reuses ids.
type HealthRecovery struct {
	ThroughEventID int64
	OccurredAt     time.Time
}

// Record appends one health event. detail must already be credential-scrubbed by
// the caller (internal/http.RedactError); this layer stores it verbatim.
func (Health) Record(ctx context.Context, q dbinterface.Execer, e domain.IndexerHealthEvent) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO indexer_health_events (instance_id, kind, detail, occurred_at)
		 VALUES (?, ?, ?, ?)`),
		e.InstanceID, e.Kind, nullIfEmpty(e.Detail), e.OccurredAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("database: record health event: %w", err)
	}
	return nil
}

// RecordRecovery advances one instance's successful-test watermark without deleting
// its failure history. The INSERT ... SELECT snapshots the current max event id in the
// same statement as the upsert, so a later failure is always distinguishable.
func (Health) RecordRecovery(ctx context.Context, q dbinterface.Execer, instanceID int64, occurredAt time.Time) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO indexer_health_recovery (instance_id, through_event_id, occurred_at)
		 SELECT ?, COALESCE(MAX(id), 0), ? FROM indexer_health_events WHERE instance_id = ?
		 ON CONFLICT(instance_id) DO UPDATE SET
		   through_event_id = excluded.through_event_id, occurred_at = excluded.occurred_at`),
		instanceID, occurredAt.UTC().Format(timeLayout), instanceID)
	if err != nil {
		return fmt.Errorf("database: record health recovery: %w", err)
	}
	return nil
}

// Recovery returns the latest successful-test watermark for an instance. An
// instance that has never passed an explicit test yields the zero value.
func (Health) Recovery(ctx context.Context, q dbinterface.Execer, instanceID int64) (HealthRecovery, error) {
	var (
		recovery HealthRecovery
		occurred string
	)
	err := q.QueryRowContext(ctx,
		q.Rebind(`SELECT through_event_id, occurred_at FROM indexer_health_recovery WHERE instance_id = ?`),
		instanceID).Scan(&recovery.ThroughEventID, &occurred)
	if errors.Is(err, sql.ErrNoRows) {
		return HealthRecovery{}, nil
	}
	if err != nil {
		return HealthRecovery{}, fmt.Errorf("database: read health recovery: %w", err)
	}
	recovery.OccurredAt = parseTime(occurred)
	return recovery, nil
}

// DeleteBefore purges every event older than cutoff, returning the number removed.
// cutoff is formatted exactly as Record stores occurred_at (a UTC RFC3339 string), so
// the lexical `occurred_at < ?` comparison is chronological. Backs the retention ticker.
func (Health) DeleteBefore(ctx context.Context, q dbinterface.Execer, cutoff time.Time) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`DELETE FROM indexer_health_events WHERE occurred_at < ?`),
		cutoff.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: delete health events before cutoff: %w", err)
	}
	return rowsAffected(res)
}

// HealthCounts is one instance's aggregated failure tally by kind plus the most
// recent failure time (zero = no failures). It backs the per-indexer stats surface,
// which folds these counts alongside the durable query/grab counters.
type HealthCounts struct {
	AuthFailure   int64
	RateLimited   int64
	ParseError    int64
	AntiBot       int64
	Transport     int64
	LastFailureAt time.Time // zero = none
}

// Counts aggregates one instance's health events by kind (and the newest failure
// time). An instance with no events yields the zero struct (all counts 0, zero time).
func (Health) Counts(ctx context.Context, q dbinterface.Execer, instanceID int64) (HealthCounts, error) {
	rows, err := q.QueryContext(ctx,
		q.Rebind(`SELECT kind, COUNT(*), MAX(occurred_at)
		 FROM indexer_health_events WHERE instance_id = ? GROUP BY kind`),
		instanceID)
	if err != nil {
		return HealthCounts{}, fmt.Errorf("database: count health events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hc HealthCounts
	for rows.Next() {
		var (
			kind   string
			count  int64
			maxOcc sql.NullString
		)
		if err := rows.Scan(&kind, &count, &maxOcc); err != nil {
			return HealthCounts{}, fmt.Errorf("database: scan health count: %w", err)
		}
		applyHealthCount(&hc, kind, count, maxOcc)
	}
	if err := rows.Err(); err != nil {
		return HealthCounts{}, fmt.Errorf("database: iterate health counts: %w", err)
	}
	return hc, nil
}

// AllCounts aggregates every instance's health events by kind in one pass, keyed by
// instance id, so the all-indexers stats endpoint avoids an N+1 per-instance query.
// Instances with no events are simply absent from the map (the caller treats a missing
// key as the zero struct).
func (Health) AllCounts(ctx context.Context, q dbinterface.Execer) (map[int64]HealthCounts, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT instance_id, kind, COUNT(*), MAX(occurred_at)
		 FROM indexer_health_events GROUP BY instance_id, kind`)
	if err != nil {
		return nil, fmt.Errorf("database: count all health events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]HealthCounts)
	for rows.Next() {
		var (
			instanceID int64
			kind       string
			count      int64
			maxOcc     sql.NullString
		)
		if err := rows.Scan(&instanceID, &kind, &count, &maxOcc); err != nil {
			return nil, fmt.Errorf("database: scan all health counts: %w", err)
		}
		hc := out[instanceID]
		applyHealthCount(&hc, kind, count, maxOcc)
		out[instanceID] = hc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate all health counts: %w", err)
	}
	return out, nil
}

// applyHealthCount folds one (kind, count, max-occurred) group into hc: it routes the
// count to the matching field and advances LastFailureAt to the newest occurrence
// across all kinds. An unrecognized kind contributes only to LastFailureAt.
func applyHealthCount(hc *HealthCounts, kind string, count int64, maxOcc sql.NullString) {
	switch kind {
	case domain.HealthAuthFailure:
		hc.AuthFailure = count
	case domain.HealthRateLimited:
		hc.RateLimited = count
	case domain.HealthParseError:
		hc.ParseError = count
	case domain.HealthAntiBot:
		hc.AntiBot = count
	case domain.HealthTransport:
		hc.Transport = count
	}
	if maxOcc.Valid {
		if occ := parseTime(maxOcc.String); occ.After(hc.LastFailureAt) {
			hc.LastFailureAt = occ
		}
	}
}

// Recent returns up to limit most-recent events for an instance, newest first.
func (Health) Recent(ctx context.Context, q dbinterface.Execer, instanceID int64, limit int) ([]domain.IndexerHealthEvent, error) {
	rows, err := q.QueryContext(ctx,
		q.Rebind(`SELECT id, instance_id, kind, detail, occurred_at
		 FROM indexer_health_events WHERE instance_id = ? ORDER BY occurred_at DESC, id DESC LIMIT ?`),
		instanceID, limit)
	if err != nil {
		return nil, fmt.Errorf("database: query health events: %w", err)
	}
	defer rows.Close()

	var out []domain.IndexerHealthEvent
	for rows.Next() {
		var (
			e      domain.IndexerHealthEvent
			detail sql.NullString
			occ    string
		)
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Kind, &detail, &occ); err != nil {
			return nil, fmt.Errorf("database: scan health event: %w", err)
		}
		e.Detail = detail.String
		e.OccurredAt = parseTime(occ)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate health events: %w", err)
	}
	return out, nil
}
