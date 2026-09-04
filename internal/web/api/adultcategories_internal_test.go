package api

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// TestAdultCategoriesSetPersistFailureKeepsLive pins the persist-before-apply order
// Setting.Write exists to make reviewable: when the app_settings write fails, Set
// returns the error and the live value is left untouched, so runtime and stored state
// can never disagree.
func TestAdultCategoriesSetPersistFailureKeepsLive(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("write failed")
	s := NewAdultCategoriesStore(failingExecer{err: wantErr}, nil)

	if err := s.Set(context.Background(), true); !errors.Is(err, wantErr) {
		t.Fatalf("Set = %v, want it to wrap the persist error", err)
	}
	if s.Hidden() {
		t.Error("a failed persist applied the value to live state anyway")
	}
}

// errExecerReadUnused marks a read call the write-only failing stub never expects.
var errExecerReadUnused = errors.New("failingExecer: read path not used")

// failingExecer is a dbinterface.Execer whose write path always errors, to exercise
// the persist-failure branch without a real database. The read methods are never
// reached on that path.
type failingExecer struct{ err error }

func (f failingExecer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, f.err
}

func (failingExecer) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errExecerReadUnused
}
func (failingExecer) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
func (failingExecer) Rebind(q string) string                                   { return q }
