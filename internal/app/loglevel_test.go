package app

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/logger"
)

// These tests mutate the process-global zerolog level (the runtime knob), so they are
// NOT parallel and each restores a permissive default for the next.

func newLogLevelApp(t *testing.T) *App {
	t.Helper()
	return &App{db: dbtest.OpenMigrated(t), log: zerolog.Nop()}
}

// TestSetLogLevelPersistsAndApplies covers the write half: the level takes effect
// process-wide, and the stored value is the bare level string the pre-Setting code
// wrote — no format change, so an existing row still reads back identically.
func TestSetLogLevelPersistsAndApplies(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)
	ctx := context.Background()
	a := newLogLevelApp(t)

	if err := a.setLogLevel(ctx, "debug"); err != nil {
		t.Fatalf("setLogLevel(debug): %v", err)
	}
	if got := logger.Level(); got != "debug" {
		t.Errorf("level = %q, want debug", got)
	}
	raw, found, err := (database.AppSettings{}).Get(ctx, a.db, logLevelSetting.Key)
	if err != nil || !found || raw != "debug" {
		t.Fatalf("stored value = %q found=%v err=%v, want %q", raw, found, err, "debug")
	}

	// A value written in the pre-Setting format reads back unchanged.
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	a.applyPersistedLogLevel(ctx)
	if got := logger.Level(); got != "debug" {
		t.Errorf("applyPersistedLogLevel left level %q, want the persisted debug", got)
	}
}

// TestApplyPersistedLogLevelFallsBack proves the fallback invariant at this call
// site: no row, or a row that is no longer a valid level, leaves the config seed in
// place instead of wedging boot.
func TestApplyPersistedLogLevelFallsBack(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)
	ctx := context.Background()

	for _, stored := range []string{"", "loud", "DEBUG!"} {
		a := newLogLevelApp(t)
		if stored != "" {
			if err := (database.AppSettings{}).Set(ctx, a.db, logLevelSetting.Key, stored, time.Now()); err != nil {
				t.Fatalf("seed %q: %v", stored, err)
			}
		}
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
		a.applyPersistedLogLevel(ctx)
		if got := logger.Level(); got != "warn" {
			t.Errorf("stored %q changed the level to %q, want the untouched seed warn", stored, got)
		}
	}
}
