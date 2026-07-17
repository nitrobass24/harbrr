package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
)

// openBudgetDB opens and migrates a file-backed DB, mirroring openCacheDB — a file path
// (not :memory:) survives across two handles, letting the persistence tests reopen to
// simulate a restart.
func openBudgetDB(t *testing.T, path string) *database.DB {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open db %q: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestRequestBudget_UnsetIsUnlimited proves that with no query_limit/grab_limit
// configured, ReserveQuery/ReserveGrab always allow — the corrected #251 premise
// that an unset budget is disabled, never an invented default.
func TestRequestBudget_UnsetIsUnlimited(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5000; i++ {
		if !b.ReserveQuery(context.Background(), instID, nil, now) {
			t.Fatalf("ReserveQuery refused on call %d with no configured limit", i)
		}
	}
}

// TestRequestBudget_ConfiguredLimitRefusesOverCap proves that with a configured
// query_limit=2000, the budget allows exactly 2000 outbound queries in the period and
// refuses the 2001st — the issue's acceptance example verbatim.
func TestRequestBudget_ConfiguredLimitRefusesOverCap(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	cfg := map[string]string{"query_limit": "2000"}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	allowed := 0
	for i := 0; i < 2005; i++ {
		if b.ReserveQuery(context.Background(), instID, cfg, now) {
			allowed++
		}
	}
	if allowed != 2000 {
		t.Fatalf("allowed = %d, want exactly 2000", allowed)
	}
	// The 2001st (and every subsequent) call within the same period must still refuse.
	if b.ReserveQuery(context.Background(), instID, cfg, now) {
		t.Fatal("ReserveQuery allowed a query past the configured cap")
	}
}

// TestRequestBudget_QueryAndGrabAreIndependent proves the query and grab counters (and
// their limits) never interfere with each other, mirroring Prowlarr's separate fields.
func TestRequestBudget_QueryAndGrabAreIndependent(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	cfg := map[string]string{"query_limit": "1", "grab_limit": "1"}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	if !b.ReserveQuery(context.Background(), instID, cfg, now) {
		t.Fatal("first query should be allowed")
	}
	if b.ReserveQuery(context.Background(), instID, cfg, now) {
		t.Fatal("second query should be refused (query_limit=1)")
	}
	// The grab budget must be untouched by the query exhaustion above.
	if !b.ReserveGrab(context.Background(), instID, cfg, now) {
		t.Fatal("first grab should still be allowed despite the query budget being spent")
	}
	if b.ReserveGrab(context.Background(), instID, cfg, now) {
		t.Fatal("second grab should be refused (grab_limit=1)")
	}
}

// TestRequestBudget_ResetsAtUTCMidnight proves the daily counter and any
// reactive-learned exhausted latch both reset the instant the UTC calendar day rolls
// over, per the issue's explicit "reset at UTC midnight for a daily unit" ask.
func TestRequestBudget_ResetsAtUTCMidnight(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	cfg := map[string]string{"query_limit": "1"}
	beforeMidnight := time.Date(2026, 7, 17, 23, 59, 59, 0, time.UTC)
	afterMidnight := time.Date(2026, 7, 18, 0, 0, 1, 0, time.UTC)

	if !b.ReserveQuery(context.Background(), instID, cfg, beforeMidnight) {
		t.Fatal("first query of the day should be allowed")
	}
	if b.ReserveQuery(context.Background(), instID, cfg, beforeMidnight) {
		t.Fatal("second query before midnight should be refused (query_limit=1)")
	}
	if !b.ReserveQuery(context.Background(), instID, cfg, afterMidnight) {
		t.Fatal("first query of the NEW UTC day should be allowed again")
	}
}

// TestRequestBudget_HourlyUnit proves limits_unit=hour keys the period to the UTC
// hour rather than the day, so a rollover an hour later resets the counter too.
func TestRequestBudget_HourlyUnit(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	cfg := map[string]string{"query_limit": "1", "limits_unit": "hour"}
	hour1 := time.Date(2026, 7, 17, 10, 30, 0, 0, time.UTC)
	hour2 := time.Date(2026, 7, 17, 11, 0, 1, 0, time.UTC)

	if !b.ReserveQuery(context.Background(), instID, cfg, hour1) {
		t.Fatal("first query of the hour should be allowed")
	}
	if b.ReserveQuery(context.Background(), instID, cfg, hour1) {
		t.Fatal("second query in the same hour should be refused")
	}
	if !b.ReserveQuery(context.Background(), instID, cfg, hour2) {
		t.Fatal("first query of the NEXT hour should be allowed again")
	}
}

// TestRequestBudget_MarkQuotaSpentLatchesEvenUnconfigured proves the reactive-learning
// path (#251's differentiator vs Prowlarr): MarkQuotaSpent refuses further requests of
// that kind for the rest of the period even with NO operator-configured limit at all —
// discovering a cap harbrr was never told about.
func TestRequestBudget_MarkQuotaSpentLatchesEvenUnconfigured(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	if !b.ReserveQuery(context.Background(), instID, nil, now) {
		t.Fatal("query should be allowed before any quota error is observed")
	}
	b.MarkQuotaSpent(context.Background(), instID, nil, budgetKindQuery, now)
	if b.ReserveQuery(context.Background(), instID, nil, now) {
		t.Fatal("query should be refused after MarkQuotaSpent, even with no configured limit")
	}
	// The grab kind is untouched by a query-kind quota mark.
	if !b.ReserveGrab(context.Background(), instID, nil, now) {
		t.Fatal("grab should still be allowed; MarkQuotaSpent was for the query kind only")
	}
	// Rolling into the next day clears the reactive-learned latch too.
	nextDay := now.Add(25 * time.Hour)
	if !b.ReserveQuery(context.Background(), instID, nil, nextDay) {
		t.Fatal("the learned-exhausted latch should reset at the next UTC midnight")
	}
}

// TestRequestBudget_PersistsAcrossRestart proves the counter and the reactive-learned
// latch both survive a process restart (a fresh RequestBudget over the same DB file),
// the durability half of the DB round-trip.
func TestRequestBudget_PersistsAcrossRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "harbrr.db")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	cfg := map[string]string{"query_limit": "1"}

	db1 := openBudgetDB(t, path)
	b1 := newRequestBudget(db1, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db1)
	if !b1.ReserveQuery(context.Background(), instID, cfg, now) {
		t.Fatal("first query should be allowed")
	}
	b1.MarkQuotaSpent(context.Background(), instID, cfg, budgetKindGrab, now)
	_ = db1.Close()

	db2, err := database.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	b2 := newRequestBudget(db2, time.Now, zerolog.Nop())

	if b2.ReserveQuery(context.Background(), instID, cfg, now) {
		t.Fatal("query budget should still read as spent (count=1, limit=1) after restart")
	}
	if b2.ReserveGrab(context.Background(), instID, cfg, now) {
		t.Fatal("grab budget should still read as reactively exhausted after restart")
	}
}
