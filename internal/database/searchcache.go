package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// SearchCacheStore is the SQLite repository for the search-results cache.
// Stateless: every method takes an Execer, so callers can pass *DB or a
// transaction handle, mirroring Instances/AppConnections.
//
// SECRETS-AT-REST: results_json holds the FULL pre-/dl-rewrite release slice,
// whose Link/Magnet embed tracker passkeys for some trackers. It is stored
// PLAINTEXT in the 0600 DB (never routed through internal/secrets) and must NEVER
// be logged. Every error here wraps ONLY cache_key or instance_id — never the
// payload, a link, or the row body — exactly as sessionstore wraps only its
// errors and never the token or data.
type SearchCacheStore struct{}

// SearchCacheEntry is one cached search result. ResultsJSON is the opaque,
// secret-bearing JSON payload (see the store doc); the store reads and writes it
// verbatim and never inspects or logs it.
type SearchCacheEntry struct {
	CacheKey     string
	InstanceID   int64
	ResultsJSON  []byte
	TotalResults int
	CachedAt     time.Time
	LastUsedAt   time.Time
	ExpiresAt    time.Time
	HitCount     int64
}

// SearchCacheStats is the process-agnostic, persisted view of the cache used by
// the management stats endpoint. The hit-ratio counters are in-memory elsewhere;
// these are the durable row-derived figures.
type SearchCacheStats struct {
	Entries         int64
	TotalHits       int64
	ApproxSizeBytes int64
	Oldest          *time.Time
	Newest          *time.Time
	LastUsed        *time.Time
}

// Fetch returns the unexpired entry for cacheKey, or found=false when absent or
// expired. It bumps nothing — the caller Touches a live hit separately so the
// read path stays a pure lookup.
func (SearchCacheStore) Fetch(ctx context.Context, q dbinterface.Execer, cacheKey string, now time.Time) (SearchCacheEntry, bool, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT cache_key, instance_id, results_json, total_results,
			cached_at, last_used_at, expires_at, hit_count
			FROM search_cache WHERE cache_key = ? AND expires_at > ?`),
		cacheKey, now.UTC().Format(timeLayout))
	e, err := scanSearchCacheEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SearchCacheEntry{}, false, nil
	}
	if err != nil {
		return SearchCacheEntry{}, false, fmt.Errorf("database: fetch search cache %q: %w", cacheKey, err)
	}
	return e, true, nil
}

// Store upserts an entry keyed on cache_key. The DO UPDATE writes everything
// EXCEPT hit_count, so a SWR refresh-write-back preserves the served-hit counter
// (Touch is its only writer). expires_at must be strictly after cached_at — a
// non-positive TTL is a caller bug and is rejected before any write.
func (SearchCacheStore) Store(ctx context.Context, q dbinterface.Execer, e SearchCacheEntry) error {
	if !e.ExpiresAt.After(e.CachedAt) {
		return fmt.Errorf("database: store search cache %q: expires_at must be after cached_at", e.CacheKey)
	}
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO search_cache
			(cache_key, instance_id, results_json, total_results, cached_at, last_used_at, expires_at, hit_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(cache_key) DO UPDATE SET
			  instance_id = excluded.instance_id,
			  results_json = excluded.results_json,
			  total_results = excluded.total_results,
			  cached_at = excluded.cached_at,
			  last_used_at = excluded.last_used_at,
			  expires_at = excluded.expires_at`),
		e.CacheKey, e.InstanceID, e.ResultsJSON, e.TotalResults,
		e.CachedAt.UTC().Format(timeLayout), e.LastUsedAt.UTC().Format(timeLayout),
		e.ExpiresAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("database: store search cache %q: %w", e.CacheKey, err)
	}
	return nil
}

// Touch records a served hit: it bumps last_used_at and increments hit_count. It
// is the sole writer of hit_count, so the counter survives refresh write-backs.
func (SearchCacheStore) Touch(ctx context.Context, q dbinterface.Execer, cacheKey string, now time.Time) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE search_cache SET last_used_at = ?, hit_count = hit_count + 1 WHERE cache_key = ?`),
		now.UTC().Format(timeLayout), cacheKey)
	if err != nil {
		return fmt.Errorf("database: touch search cache %q: %w", cacheKey, err)
	}
	return nil
}

// CleanupExpired deletes every entry whose expires_at is at or before now,
// returning the number purged. The background ticker calls it periodically.
func (SearchCacheStore) CleanupExpired(ctx context.Context, q dbinterface.Execer, now time.Time) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`DELETE FROM search_cache WHERE expires_at <= ?`), now.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: cleanup expired search cache: %w", err)
	}
	return rowsAffected(res)
}

// Flush deletes all entries, returning the number purged. Backs the management
// flush endpoint.
func (SearchCacheStore) Flush(ctx context.Context, q dbinterface.Execer) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM search_cache`)
	if err != nil {
		return 0, fmt.Errorf("database: flush search cache: %w", err)
	}
	return rowsAffected(res)
}

// InvalidateByInstance deletes every entry for one instance, returning the number
// purged. Update/SetEnabled call it so a config change never serves stale results.
func (SearchCacheStore) InvalidateByInstance(ctx context.Context, q dbinterface.Execer, instanceID int64) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`DELETE FROM search_cache WHERE instance_id = ?`), instanceID)
	if err != nil {
		return 0, fmt.Errorf("database: invalidate search cache for instance %d: %w", instanceID, err)
	}
	return rowsAffected(res)
}

// Stats returns the durable, row-derived cache figures for the stats endpoint. An
// empty cache yields zero counts and nil timestamps.
func (SearchCacheStore) Stats(ctx context.Context, q dbinterface.Execer) (SearchCacheStats, error) {
	row := q.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(hit_count), 0), COALESCE(SUM(LENGTH(results_json)), 0),
			MIN(cached_at), MAX(cached_at), MAX(last_used_at)
			FROM search_cache`)
	var (
		s                        SearchCacheStats
		oldest, newest, lastUsed sql.NullString
	)
	if err := row.Scan(&s.Entries, &s.TotalHits, &s.ApproxSizeBytes, &oldest, &newest, &lastUsed); err != nil {
		return SearchCacheStats{}, fmt.Errorf("database: search cache stats: %w", err)
	}
	s.Oldest, s.Newest, s.LastUsed = timePtr(oldest), timePtr(newest), timePtr(lastUsed)
	return s, nil
}

// scanSearchCacheEntry reads one search_cache row from a *sql.Row or *sql.Rows.
func scanSearchCacheEntry(s interface{ Scan(...any) error }) (SearchCacheEntry, error) {
	var (
		e                               SearchCacheEntry
		cachedAt, lastUsedAt, expiresAt string
	)
	if err := s.Scan(&e.CacheKey, &e.InstanceID, &e.ResultsJSON, &e.TotalResults,
		&cachedAt, &lastUsedAt, &expiresAt, &e.HitCount); err != nil {
		return SearchCacheEntry{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	e.CachedAt = parseTime(cachedAt)
	e.LastUsedAt = parseTime(lastUsedAt)
	e.ExpiresAt = parseTime(expiresAt)
	return e, nil
}

// rowsAffected reads a delete/update's affected count, wrapping any driver error.
func rowsAffected(res sql.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("database: rows affected: %w", err)
	}
	return n, nil
}
