package database

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// Setting is one typed app_settings entry: its key, its wire format, and the value
// that stands in when nothing usable is stored. It sits on top of AppSettings (the
// untyped string KV store) and owns the two rules every persisted setting follows —
// rules that were previously prose, re-implemented once per consumer:
//
//   - Read NEVER fails. A missing row, an unreadable database, or a value Parse
//     rejects all yield Default; the miss is logged and the caller moves on. That is
//     why Read returns T and not (T, error): a stale or hand-edited row must never be
//     able to wedge boot.
//   - Write persists ONLY. The caller applies the value to live state AFTER Write
//     returns nil, so a failed write leaves runtime untouched and the stored and
//     running values can never disagree. Write deliberately takes no apply callback:
//     keeping the apply at the call site is what makes that ordering reviewable.
//
// Parse doubles as validation — return an error for any stored value the setting
// should refuse (out of range, no longer a member of the enum) and Read falls back to
// Default. Format must round-trip through Parse, and its output is the on-disk wire
// format, so changing it is a migration.
type Setting[T any] struct {
	Key     string
	Default T
	Parse   func(string) (T, error)
	Format  func(T) string
}

// Read returns the stored value, or Default when there is no row, the read fails, or
// the stored value does not parse. It never returns an error — see the type doc.
func (s Setting[T]) Read(ctx context.Context, q dbinterface.Execer, log zerolog.Logger) T {
	raw, found, err := AppSettings{}.Get(ctx, q, s.Key)
	switch {
	case err != nil:
		log.Warn().Err(err).Str("setting", s.Key).Msg("database: reading the setting failed; using the default")
		return s.Default
	case !found:
		return s.Default
	}
	v, err := s.Parse(raw)
	if err != nil {
		log.Warn().Err(err).Str("setting", s.Key).Msg("database: the stored setting is unusable; using the default")
		return s.Default
	}
	return v
}

// Write persists v in its wire format, stamping updatedAt. It does NOT touch live
// state — the caller applies the value only after this returns nil.
func (s Setting[T]) Write(ctx context.Context, q dbinterface.Execer, v T, updatedAt time.Time) error {
	return AppSettings{}.Set(ctx, q, s.Key, s.Format(v), updatedAt)
}
