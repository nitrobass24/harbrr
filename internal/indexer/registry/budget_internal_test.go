package registry

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
)

// openBudgetDB opens a migrated file-backed DB, mirroring openCacheDB — a file path
// (not :memory:) survives across two handles, letting the persistence tests reopen to
// simulate a restart (an existing file is reopened with its data intact).
func openBudgetDB(t *testing.T, path string) *database.DB {
	t.Helper()
	return dbtest.OpenMigratedAt(t, path)
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

	for i := range 5000 {
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
	for range 2005 {
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

// TestRequestBudget_ReleaseRefundsWithinPeriod proves the refund's three guards in one
// place: it gives a unit back inside the reservation's own period, it never drives the
// counter below zero, and it leaves the reactively-learned exhausted latch alone — a
// unit handed back is not evidence the tracker's cap lifted.
func TestRequestBudget_ReleaseRefundsWithinPeriod(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	if !b.ReserveQuery(ctx, instID, nil, now) {
		t.Fatal("query should be allowed with no configured limit")
	}
	b.MarkQuotaSpent(ctx, instID, nil, budgetKindQuery, now)
	b.ReleaseQuery(ctx, instID, nil, now)

	st := b.Status(ctx, instID, nil, now)
	if st.Query.Used != 0 {
		t.Fatalf("query used = %d after the refund, want 0", st.Query.Used)
	}
	if !st.Query.Learned {
		t.Fatal("the learned-exhausted latch must survive a refund")
	}

	// A refund with nothing outstanding must not push the counter negative.
	b.ReleaseQuery(ctx, instID, nil, now)
	b.ReleaseGrab(ctx, instID, nil, now)
	st = b.Status(ctx, instID, nil, now)
	if st.Query.Used != 0 || st.Grab.Used != 0 {
		t.Fatalf("used = (query %d, grab %d) after refunding nothing, want 0/0", st.Query.Used, st.Grab.Used)
	}
}

// TestRequestBudget_ReleaseAfterRolloverKeepsNewPeriod proves a refund arriving after
// the period rolled over is dropped rather than applied: the reservation it refers to
// died with its period, so decrementing would steal from the fresh period's allowance.
func TestRequestBudget_ReleaseAfterRolloverKeepsNewPeriod(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	ctx := context.Background()
	beforeMidnight := time.Date(2026, 7, 17, 23, 59, 59, 0, time.UTC)
	afterMidnight := time.Date(2026, 7, 18, 0, 0, 1, 0, time.UTC)

	if !b.ReserveQuery(ctx, instID, nil, beforeMidnight) {
		t.Fatal("reserve before midnight should be allowed")
	}
	// The new day's first query rolls the counter; the old reservation is already gone.
	if !b.ReserveQuery(ctx, instID, nil, afterMidnight) {
		t.Fatal("reserve after midnight should be allowed")
	}
	b.ReleaseQuery(ctx, instID, nil, beforeMidnight)

	if got := b.Status(ctx, instID, nil, afterMidnight).Query.Used; got != 1 {
		t.Fatalf("new-period query used = %d after a stale refund, want 1", got)
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

// TestRequestBudget_StatusReportsCurrentPeriod proves the read accessor behind the
// usage meter (autobrr/harbrr#402) reports the live count, the configured cap, the
// learned latch (kept separate from the configured cap), the unit, and the period
// boundary — for both the day and the hour unit.
func TestRequestBudget_StatusReportsCurrentPeriod(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name          string
		cfg           map[string]string
		reserveQuery  int
		markGrabSpent bool
		want          BudgetStatus
	}{
		{
			name:         "configured day cap counts up",
			cfg:          map[string]string{"query_limit": "2000"},
			reserveQuery: 3,
			want: BudgetStatus{
				Unit:      "day",
				PeriodEnd: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
				Query:     BudgetKindStatus{Used: 3, Limit: 2000},
			},
		},
		{
			name: "hour unit ends at the top of the next hour",
			cfg:  map[string]string{"query_limit": "150", "limits_unit": "hour"},
			want: BudgetStatus{
				Unit:      "hour",
				PeriodEnd: time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC),
				Query:     BudgetKindStatus{Limit: 150},
			},
		},
		{
			name:          "learned latch is reported without a configured cap",
			cfg:           nil,
			markGrabSpent: true,
			want: BudgetStatus{
				Unit:      "day",
				PeriodEnd: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
				Grab:      BudgetKindStatus{Learned: true},
			},
		},
		{
			name: "a seeded cap reports its detected provenance per kind",
			cfg: map[string]string{
				"query_limit": "2000", "query_limit_source": "detected",
				"grab_limit": "10",
			},
			want: BudgetStatus{
				Unit:      "day",
				PeriodEnd: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
				Query:     BudgetKindStatus{Limit: 2000, Detected: true},
				Grab:      BudgetKindStatus{Limit: 10},
			},
		},
		{
			name: "a detected marker without a cap reports nothing",
			cfg:  map[string]string{"query_limit_source": "detected"},
			want: BudgetStatus{
				Unit:      "day",
				PeriodEnd: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "untracked indexer reads all zeroes",
			cfg:  nil,
			want: BudgetStatus{
				Unit:      "day",
				PeriodEnd: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
			b := newRequestBudget(db, time.Now, zerolog.Nop())
			instID := insertTestInstance(t, db)
			for i := 0; i < tt.reserveQuery; i++ {
				b.ReserveQuery(context.Background(), instID, tt.cfg, now)
			}
			if tt.markGrabSpent {
				b.MarkQuotaSpent(context.Background(), instID, tt.cfg, budgetKindGrab, now)
			}
			if got := b.Status(context.Background(), instID, tt.cfg, now); got != tt.want {
				t.Errorf("Status = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestRequestBudget_StatusIsReadOnly proves the meter's read neither counts a request
// nor writes a row: an untouched instance stays absent from the store, and the budget
// it reported on is still fully available afterwards.
func TestRequestBudget_StatusIsReadOnly(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	cfg := map[string]string{"query_limit": "1"}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	for range 3 {
		b.Status(context.Background(), instID, cfg, now)
	}
	if _, found, err := (database.BudgetCountersStore{}).Get(context.Background(), db, instID); err != nil || found {
		t.Fatalf("Status persisted a row for an untouched instance: found=%v err=%v", found, err)
	}
	if !b.ReserveQuery(context.Background(), instID, cfg, now) {
		t.Fatal("Status consumed budget: the single configured query was already spent")
	}
}

// TestRequestBudget_StatusRollsOverAndReloads proves two reads the meter depends on: a
// state left behind in an expired period reports a fresh period (not yesterday's
// count), and a fresh process reports the DURABLE counters rather than a false zero.
func TestRequestBudget_StatusRollsOverAndReloads(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "harbrr.db")
	cfg := map[string]string{"query_limit": "2000"}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	db1 := openBudgetDB(t, path)
	b1 := newRequestBudget(db1, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db1)
	b1.ReserveQuery(context.Background(), instID, cfg, now)
	b1.MarkQuotaSpent(context.Background(), instID, cfg, budgetKindQuery, now)

	nextDay := now.Add(24 * time.Hour)
	if got := b1.Status(context.Background(), instID, cfg, nextDay); got.Query.Used != 0 || got.Query.Learned {
		t.Errorf("Status after rollover = %+v, want a fresh period (0 used, no latch)", got.Query)
	}
	// The rollover read must not have written it back — the OLD period is still what is
	// stored, so a same-period reader still sees the real count.
	if got := b1.Status(context.Background(), instID, cfg, now); got.Query.Used != 1 || !got.Query.Learned {
		t.Errorf("Status in the original period = %+v, want used=1 with the learned latch", got.Query)
	}
	_ = db1.Close()

	db2, err := database.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	b2 := newRequestBudget(db2, time.Now, zerolog.Nop())
	if got := b2.Status(context.Background(), instID, cfg, now); got.Query.Used != 1 || !got.Query.Learned {
		t.Errorf("Status after restart = %+v, want the durable used=1 + learned latch", got.Query)
	}
}

// TestRequestBudget_PersistOrderUnderConcurrency proves the store snapshot is written
// under the same per-instance lock as the in-memory mutation: after concurrent
// reserves the persisted row carries the FINAL count and the exhausted latch, never a
// stale intermediate that an out-of-order late write left behind (which a restart
// would reload as an undercount, or worse, a dropped latch).
func TestRequestBudget_PersistOrderUnderConcurrency(t *testing.T) {
	t.Parallel()
	db := openBudgetDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	b := newRequestBudget(db, time.Now, zerolog.Nop())
	instID := insertTestInstance(t, db)
	cfg := map[string]string{"query_limit": "64"}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	var allows atomic.Int64
	for range 128 {
		wg.Go(func() {
			if b.ReserveQuery(context.Background(), instID, cfg, now) {
				allows.Add(1)
			}
		})
	}
	wg.Wait()

	if got := allows.Load(); got != 64 {
		t.Fatalf("allowed %d concurrent queries, want exactly the configured limit 64", got)
	}
	row, found, err := (database.BudgetCountersStore{}).Get(context.Background(), db, instID)
	if err != nil || !found {
		t.Fatalf("Get persisted row: found=%v err=%v", found, err)
	}
	if row.QueryCount != 64 {
		t.Fatalf("persisted QueryCount = %d, want 64 (a stale snapshot won the write race)", row.QueryCount)
	}

	b.MarkQuotaSpent(context.Background(), instID, cfg, budgetKindQuery, now)
	row, _, err = (database.BudgetCountersStore{}).Get(context.Background(), db, instID)
	if err != nil {
		t.Fatalf("Get after MarkQuotaSpent: %v", err)
	}
	if !row.QueryExhausted {
		t.Fatal("persisted QueryExhausted = false after MarkQuotaSpent")
	}
}
