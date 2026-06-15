package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Health is the SQLite repository for the append-only indexer_health_events table.
// Stateless: every method takes an Execer (so it runs standalone or in a tx) and
// routes every placeholder through q.Rebind. Mirrors AppMeta/Instances.
type Health struct{}

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
