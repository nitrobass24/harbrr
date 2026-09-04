package database_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
)

// countSetting is a stand-in for any bounded numeric setting: Parse doubles as
// validation, so "0" is stored-but-refused rather than accepted.
var countSetting = database.Setting[int]{
	Key:     "test.count",
	Default: 12,
	Parse: func(raw string) (int, error) {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, err
		}
		if n <= 0 {
			return 0, errors.New("count must be positive")
		}
		return n, nil
	},
	Format: strconv.Itoa,
}

// TestSettingReadFallsBackToDefault is THE test for the invariant Setting exists to
// own, and which every consumer used to re-implement: a missing, malformed, refused
// or unreadable row yields Default. Read has no error return, so there is no path by
// which a bad row can wedge boot.
func TestSettingReadFallsBackToDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := zerolog.Nop()

	tests := []struct {
		name   string
		stored string // "" means: write no row at all
	}{
		{name: "missing row"},
		{name: "empty value", stored: " "},
		{name: "not a number", stored: "twelve"},
		{name: "refused by Parse", stored: "0"},
		{name: "refused by Parse (negative)", stored: "-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := dbtest.OpenMigrated(t)
			if tt.stored != "" {
				if err := (database.AppSettings{}).Set(ctx, db, countSetting.Key, tt.stored, time.Now()); err != nil {
					t.Fatalf("seed %q: %v", tt.stored, err)
				}
			}
			if got := countSetting.Read(ctx, db, log); got != countSetting.Default {
				t.Errorf("Read = %d, want the default %d", got, countSetting.Default)
			}
		})
	}

	t.Run("unreadable database", func(t *testing.T) {
		t.Parallel()
		db := dbtest.OpenMigrated(t)
		if err := countSetting.Write(ctx, db, 7, time.Now()); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_ = db.Close() // a usable row is stored, but the handle can no longer reach it
		if got := countSetting.Read(ctx, db, log); got != countSetting.Default {
			t.Errorf("Read on a closed database = %d, want the default %d", got, countSetting.Default)
		}
	})
}

// TestSettingWriteRoundTrip proves Write stores exactly Format's output — the wire
// format is the caller's, not Setting's — and that Read returns it unchanged.
func TestSettingWriteRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.OpenMigrated(t)

	if err := countSetting.Write(ctx, db, 7, time.Now()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, found, err := (database.AppSettings{}).Get(ctx, db, countSetting.Key)
	if err != nil || !found || raw != "7" {
		t.Fatalf("stored value = %q found=%v err=%v, want %q", raw, found, err, "7")
	}
	if got := countSetting.Read(ctx, db, zerolog.Nop()); got != 7 {
		t.Errorf("Read = %d, want 7", got)
	}
}

// TestSettingWriteReportsFailure is the other half of persist-before-apply: Write
// surfaces the error so the caller can skip its apply step. Write touches nothing
// but the row — there is no live state for it to have half-updated.
func TestSettingWriteReportsFailure(t *testing.T) {
	t.Parallel()
	db := dbtest.OpenMigrated(t)
	_ = db.Close()
	if err := countSetting.Write(context.Background(), db, 3, time.Now()); err == nil {
		t.Fatal("Write to a closed database returned nil, want an error the caller can act on")
	}
}
