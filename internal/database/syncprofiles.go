package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// SyncProfiles is the SQLite repository for named sync-profile (routing set)
// resources (the Prowlarr AppProfile equivalent, narrowed to routing by #365).
// Stateless like the other resource repos: every method takes an Execer, so it runs
// standalone or inside a transaction. It holds no secrets.
type SyncProfiles struct{}

// profileColumns is the full select list, in scan order. The pre-#365 behavioral
// columns (categories, min_seeders, enable_*) still exist in the DB with defaults —
// this repo simply no longer reads or writes them.
const profileColumns = `id, name, created_at, updated_at`

// InsertProfile writes a sync-profile row and returns its new id.
func (SyncProfiles) InsertProfile(ctx context.Context, q dbinterface.Execer, p domain.SyncProfile) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO sync_profiles (name, created_at, updated_at) VALUES (?, ?, ?)`),
		p.Name, p.CreatedAt.UTC().Format(timeLayout), p.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert sync profile: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: sync profile last insert id: %w", err)
	}
	return id, nil
}

// GetProfile returns the sync profile with the given id (its IndexerIDs hydrated), or
// ErrNotFound.
func (SyncProfiles) GetProfile(ctx context.Context, q dbinterface.Execer, id int64) (domain.SyncProfile, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+profileColumns+` FROM sync_profiles WHERE id = ?`), id)
	p, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SyncProfile{}, fmt.Errorf("sync profile %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.SyncProfile{}, fmt.Errorf("database: scan sync profile %d: %w", id, err)
	}
	ids, err := (SyncProfiles{}).ListProfileIndexers(ctx, q, id)
	if err != nil {
		return domain.SyncProfile{}, err
	}
	p.IndexerIDs = ids
	return p, nil
}

// ListProfiles returns all sync profiles ordered by id, each with its IndexerIDs hydrated.
func (SyncProfiles) ListProfiles(ctx context.Context, q dbinterface.Execer) ([]domain.SyncProfile, error) {
	out, err := queryProfiles(ctx, q)
	if err != nil {
		return nil, err
	}
	byProfile, err := (SyncProfiles{}).ListAllProfileIndexers(ctx, q)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].IndexerIDs = byProfile[out[i].ID]
	}
	return out, nil
}

// queryProfiles reads the sync_profiles rows (no IndexerIDs) into a slice, closing its
// own Rows before returning — split out of ListProfiles so that Rows is guaranteed
// closed before the caller's follow-up ListAllProfileIndexers query, avoiding a
// self-deadlock against the single-connection pool (db.go's SetMaxOpenConns(1)).
func queryProfiles(ctx context.Context, q dbinterface.Execer) ([]domain.SyncProfile, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+profileColumns+` FROM sync_profiles ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list sync profiles: %w", err)
	}
	defer rows.Close()

	var out []domain.SyncProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("database: scan sync profile row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate sync profiles: %w", err)
	}
	return out, nil
}

// UpdateProfile writes a profile's mutable fields by id, returning ErrNotFound when
// no row matches. The indexer selection is written separately (ReplaceProfileIndexers),
// in the same transaction, by the caller.
func (SyncProfiles) UpdateProfile(ctx context.Context, q dbinterface.Execer, p domain.SyncProfile) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE sync_profiles SET name = ?, updated_at = ? WHERE id = ?`),
		p.Name, p.UpdatedAt.UTC().Format(timeLayout), p.ID)
	if err != nil {
		return fmt.Errorf("database: update sync profile: %w", err)
	}
	return affectedOrNotFoundID(res, p.ID)
}

// DeleteProfile removes a sync profile by id, returning ErrNotFound when absent. Its
// sync_profile_indexers rows cascade; a referencing connection's sync_profile_id is
// nulled by the ON DELETE SET NULL FK (the appsync service refuses the delete first
// while any connection still references it — see appsync.DeleteProfile).
func (SyncProfiles) DeleteProfile(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM sync_profiles WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete sync profile: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// ReplaceProfileIndexers overwrites a profile's selected-instance set: delete then
// insert, inside the caller's transaction, so a concurrent read never sees a
// partially-replaced set.
func (SyncProfiles) ReplaceProfileIndexers(ctx context.Context, q dbinterface.Execer, profileID int64, instanceIDs []int64) error {
	if _, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM sync_profile_indexers WHERE profile_id = ?`), profileID); err != nil {
		return fmt.Errorf("database: clear sync profile indexers: %w", err)
	}
	for _, instID := range instanceIDs {
		if _, err := q.ExecContext(ctx,
			q.Rebind(`INSERT INTO sync_profile_indexers (profile_id, instance_id) VALUES (?, ?)`),
			profileID, instID); err != nil {
			return fmt.Errorf("database: insert sync profile indexer: %w", err)
		}
	}
	return nil
}

// ListProfileIndexers returns a profile's selected instance ids, ordered, never nil.
func (SyncProfiles) ListProfileIndexers(ctx context.Context, q dbinterface.Execer, profileID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx,
		q.Rebind(`SELECT instance_id FROM sync_profile_indexers WHERE profile_id = ? ORDER BY instance_id`), profileID)
	if err != nil {
		return nil, fmt.Errorf("database: list sync profile indexers: %w", err)
	}
	defer rows.Close()

	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("database: scan sync profile indexer: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate sync profile indexers: %w", err)
	}
	return out, nil
}

// ListAllProfileIndexers returns every profile's selected instance ids in one query
// (no N+1 for ListProfiles), grouped by profile id.
func (SyncProfiles) ListAllProfileIndexers(ctx context.Context, q dbinterface.Execer) (map[int64][]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT profile_id, instance_id FROM sync_profile_indexers ORDER BY profile_id, instance_id`)
	if err != nil {
		return nil, fmt.Errorf("database: list all sync profile indexers: %w", err)
	}
	defer rows.Close()

	out := map[int64][]int64{}
	for rows.Next() {
		var profileID, instID int64
		if err := rows.Scan(&profileID, &instID); err != nil {
			return nil, fmt.Errorf("database: scan sync profile indexer: %w", err)
		}
		out[profileID] = append(out[profileID], instID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate sync profile indexers: %w", err)
	}
	return out, nil
}

// scanProfile reads one sync_profiles row from a *sql.Row or *sql.Rows. IndexerIDs is
// hydrated separately by the caller (Get/List) — the join lives in its own table now.
func scanProfile(sc interface{ Scan(...any) error }) (domain.SyncProfile, error) {
	var (
		p                    domain.SyncProfile
		createdAt, updatedAt string
	)
	if err := sc.Scan(&p.ID, &p.Name, &createdAt, &updatedAt); err != nil {
		return domain.SyncProfile{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	p.CreatedAt, p.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return p, nil
}

// encodeCategoryIDs joins category ids into the stored comma-separated form (empty
// slice → "").
func encodeCategoryIDs(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// decodeCategoryIDs parses the stored comma-separated form back into ids. An empty
// string decodes to an empty (non-nil) slice; a malformed token is skipped (values are
// written by us, so this is defensive).
func decodeCategoryIDs(s string) []int {
	if s == "" {
		return []int{}
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}
