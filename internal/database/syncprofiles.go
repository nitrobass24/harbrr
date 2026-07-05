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

// SyncProfiles is the SQLite repository for named sync-profile resources (the
// Prowlarr AppProfile equivalent). Stateless like the other resource repos: every
// method takes an Execer, so it runs standalone or inside a transaction. It holds no
// secrets — a profile is just a category subset plus push overrides.
type SyncProfiles struct{}

// profileColumns is the full select list, in scan order.
const profileColumns = `id, name, categories, min_seeders,
	enable_rss, enable_automatic_search, enable_interactive_search, created_at, updated_at`

// InsertProfile writes a sync-profile row and returns its new id.
func (SyncProfiles) InsertProfile(ctx context.Context, q dbinterface.Execer, p domain.SyncProfile) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO sync_profiles
			(name, categories, min_seeders, enable_rss, enable_automatic_search, enable_interactive_search, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		p.Name, encodeCategoryIDs(p.Categories), p.MinSeeders,
		boolToInt(p.EnableRss), boolToInt(p.EnableAutomaticSearch), boolToInt(p.EnableInteractiveSearch),
		p.CreatedAt.UTC().Format(timeLayout), p.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert sync profile: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: sync profile last insert id: %w", err)
	}
	return id, nil
}

// GetProfile returns the sync profile with the given id, or ErrNotFound.
func (SyncProfiles) GetProfile(ctx context.Context, q dbinterface.Execer, id int64) (domain.SyncProfile, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+profileColumns+` FROM sync_profiles WHERE id = ?`), id)
	p, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SyncProfile{}, fmt.Errorf("sync profile %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.SyncProfile{}, err
	}
	return p, nil
}

// ListProfiles returns all sync profiles ordered by id.
func (SyncProfiles) ListProfiles(ctx context.Context, q dbinterface.Execer) ([]domain.SyncProfile, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+profileColumns+` FROM sync_profiles ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list sync profiles: %w", err)
	}
	defer rows.Close()

	var out []domain.SyncProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate sync profiles: %w", err)
	}
	return out, nil
}

// UpdateProfile writes a profile's mutable fields by id, returning ErrNotFound when
// no row matches.
func (SyncProfiles) UpdateProfile(ctx context.Context, q dbinterface.Execer, p domain.SyncProfile) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE sync_profiles SET
			name = ?, categories = ?, min_seeders = ?,
			enable_rss = ?, enable_automatic_search = ?, enable_interactive_search = ?, updated_at = ?
			WHERE id = ?`),
		p.Name, encodeCategoryIDs(p.Categories), p.MinSeeders,
		boolToInt(p.EnableRss), boolToInt(p.EnableAutomaticSearch), boolToInt(p.EnableInteractiveSearch),
		p.UpdatedAt.UTC().Format(timeLayout), p.ID)
	if err != nil {
		return fmt.Errorf("database: update sync profile: %w", err)
	}
	return affectedOrNotFoundID(res, p.ID)
}

// DeleteProfile removes a sync profile by id, returning ErrNotFound when absent.
// Referencing connections' sync_profile_id is nulled by the ON DELETE SET NULL FK.
func (SyncProfiles) DeleteProfile(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM sync_profiles WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete sync profile: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// scanProfile reads one sync_profiles row from a *sql.Row or *sql.Rows.
func scanProfile(sc interface{ Scan(...any) error }) (domain.SyncProfile, error) {
	var (
		p                            domain.SyncProfile
		categories                   string
		rss, autoSearch, interactive int
		createdAt, updatedAt         string
	)
	if err := sc.Scan(&p.ID, &p.Name, &categories, &p.MinSeeders,
		&rss, &autoSearch, &interactive, &createdAt, &updatedAt); err != nil {
		return domain.SyncProfile{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	p.Categories = decodeCategoryIDs(categories)
	p.EnableRss, p.EnableAutomaticSearch, p.EnableInteractiveSearch = rss != 0, autoSearch != 0, interactive != 0
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
