package api

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/logger"
)

// These tests mutate the process-global zerolog level (the runtime knob), so they are
// NOT parallel and each restores a permissive default for the next.

func TestLogLevelStoreSetAndPersist(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)
	ctx := context.Background()
	db := openLogLevelDB(t)

	s := NewLogLevelStore(db, nil)

	// Set applies globally and persists.
	if err := s.Set(ctx, "debug"); err != nil {
		t.Fatalf("Set(debug): %v", err)
	}
	if got := s.Current(); got != "debug" {
		t.Errorf("Current() = %q, want debug", got)
	}

	// An invalid level is rejected and changes nothing.
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if err := s.Set(ctx, "loud"); !errors.Is(err, errInvalidLogLevel) {
		t.Fatalf("Set(loud) = %v, want errInvalidLogLevel", err)
	}
	if got := logger.Level(); got != "info" {
		t.Errorf("rejected Set changed level to %q, want unchanged info", got)
	}

	// ApplyPersisted restores the persisted "debug" over the current seed.
	applied, err := s.ApplyPersisted(ctx)
	if err != nil {
		t.Fatalf("ApplyPersisted: %v", err)
	}
	if !applied || logger.Level() != "debug" {
		t.Errorf("ApplyPersisted applied=%v level=%q, want true/debug", applied, logger.Level())
	}
}

func TestLogLevelStoreApplyNoOverride(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)
	ctx := context.Background()
	db := openLogLevelDB(t)

	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	applied, err := NewLogLevelStore(db, nil).ApplyPersisted(ctx)
	if err != nil {
		t.Fatalf("ApplyPersisted: %v", err)
	}
	if applied {
		t.Error("ApplyPersisted applied=true with no stored override, want false")
	}
	if got := logger.Level(); got != "warn" {
		t.Errorf("level = %q, want unchanged warn (no override to apply)", got)
	}
}

// TestLogLevelStoreSetPersistFailureKeepsLevel proves the persist-before-apply order:
// when the app_settings write fails, Set returns the error and the running level is left
// untouched (never desynchronized from the persisted state).
func TestLogLevelStoreSetPersistFailureKeepsLevel(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	wantErr := errors.New("write failed")
	s := NewLogLevelStore(failingExecer{err: wantErr}, nil)

	if err := s.Set(context.Background(), "debug"); !errors.Is(err, wantErr) {
		t.Fatalf("Set = %v, want it to wrap the persist error", err)
	}
	if got := logger.Level(); got != "info" {
		t.Errorf("level = %q after a failed persist, want unchanged info", got)
	}
}

// errExecerReadUnused marks a read call the write-only failing stub never expects.
var errExecerReadUnused = errors.New("failingExecer: read path not used")

// failingExecer is a dbinterface.Execer whose write path always errors, to exercise the
// persist-failure branch of LogLevelStore.Set without a real database. The read methods
// are never reached on that path.
type failingExecer struct{ err error }

func (f failingExecer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, f.err
}

func (failingExecer) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errExecerReadUnused
}
func (failingExecer) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
func (failingExecer) Rebind(q string) string                                   { return q }

func openLogLevelDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
