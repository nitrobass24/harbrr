package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// TestParentCategoryCounts proves the folding rule the durable table depends on:
// results are counted under the standard FAMILY root, custom (1:1) ids fold to their
// standard sibling when the release carries one, and a release with nothing mappable
// lands in the uncategorized bucket instead of being dropped.
func TestParentCategoryCounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		releases []*normalizer.Release
		want     map[int]int64
	}{
		{name: "no releases", releases: nil, want: nil},
		{
			name: "children fold onto their family root",
			releases: []*normalizer.Release{
				{Categories: []int{2040}}, {Categories: []int{2045}}, {Categories: []int{5070}},
			},
			want: map[int]int64{2000: 2, 5000: 1},
		},
		{
			name:     "a parent id counts as itself",
			releases: []*normalizer.Release{{Categories: []int{3000}}},
			want:     map[int]int64{3000: 1},
		},
		{
			name:     "a custom id alongside a standard one uses the standard sibling",
			releases: []*normalizer.Release{{Categories: []int{7020, mapper.CustomCategoryOffset + 42}}},
			want:     map[int]int64{7000: 1},
		},
		{
			name: "custom-only and category-less releases are counted, not dropped",
			releases: []*normalizer.Release{
				{Categories: []int{mapper.CustomCategoryOffset + 42}}, {Categories: nil}, nil,
			},
			want: map[int]int64{mapper.UncategorizedID: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parentCategoryCounts(tt.releases)
			if len(got) != len(tt.want) {
				t.Fatalf("counts = %v, want %v", got, tt.want)
			}
			for cat, n := range tt.want {
				if got[cat] != n {
					t.Errorf("counts[%d] = %d, want %d", cat, got[cat], n)
				}
			}
		})
	}
}

// TestCategoryStatsFlushRoundTrip proves the delta contract end to end: recorded
// results/grabs reach the current month's row on flush, the in-memory tallies are left
// at zero so the next flush cannot double-count them, and a second round of increments
// accumulates on top.
func TestCategoryStatsFlushRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")
	s := newStats(db)
	if err := s.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}

	s.RecordCategoryResults(id, map[int]int64{2000: 12, 5000: 3})
	s.RecordGrabAttempt(id)
	s.RecordGrab(id, 2000)
	s.FlushCounters(ctx)
	s.FlushCounters(ctx) // the drained deltas must not be written twice

	s.RecordCategoryResults(id, map[int]int64{2000: 1})
	s.FlushCounters(ctx)

	got, err := (database.IndexerCategoryStatsStore{}).Tallies(ctx, db)
	if err != nil {
		t.Fatalf("Tallies: %v", err)
	}
	want := []database.IndexerCategoryCount{
		{InstanceID: id, CategoryID: 2000, QueryResults: 13, Grabs: 1},
		{InstanceID: id, CategoryID: 5000, QueryResults: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("tallies = %+v, want %+v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("tally[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestCategoryStatsFlushRetriesFailedDeltas proves an increment that could not land is
// folded back into memory rather than lost: the row for a deleted instance fails, the
// surviving instance's row commits, and the failed delta is written on a later flush.
func TestCategoryStatsFlushRetriesFailedDeltas(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")
	s := newStats(db)
	if err := s.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}

	s.RecordCategoryResults(9999, map[int]int64{2000: 4}) // no such instance: FK violation
	s.RecordCategoryResults(id, map[int]int64{2000: 4})
	s.FlushCounters(ctx)

	if got := s.get(9999).cat(2000).queryResults.Load(); got != 4 {
		t.Errorf("failed delta = %d, want 4 held for retry", got)
	}
	if got := s.get(id).cat(2000).queryResults.Load(); got != 0 {
		t.Errorf("committed delta = %d, want 0 (drained)", got)
	}
}

// TestReapCategoryStats proves retention: buckets older than the configured window are
// range-deleted and the window itself is honored.
func TestReapCategoryStats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")
	now := statsClock()
	r := &StatsReporter{db: db, clock: func() time.Time { return now }}
	store := database.IndexerCategoryStatsStore{}

	for _, months := range []int{0, -2, -7} {
		bucket := database.MonthBucket(now.AddDate(0, months, 0))
		if _, err := store.AddDeltas(ctx, db, bucket, []database.IndexerCategoryCount{
			{InstanceID: id, CategoryID: 2000, QueryResults: 1},
		}, now); err != nil {
			t.Fatalf("seed %s: %v", bucket, err)
		}
	}

	// Default window (12 months) keeps everything.
	if deleted, err := r.ReapCategoryStats(ctx); err != nil || deleted != 0 {
		t.Fatalf("reap at the default window = %d / %v, want 0 deleted", deleted, err)
	}
	if err := r.SetCategoryStatsRetention(ctx, 3); err != nil {
		t.Fatalf("SetCategoryStatsRetention: %v", err)
	}
	deleted, err := r.ReapCategoryStats(ctx)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the 7-month-old bucket)", deleted)
	}
	if err := r.SetCategoryStatsRetention(ctx, 0); err == nil {
		t.Error("SetCategoryStatsRetention(0) succeeded, want an invalid-input error")
	}
}

// TestBuildIndexerStatDerivesRates proves both derived values are computed at read time
// and that neither divides by zero: no queries means no average, and no grab ATTEMPTS
// means no rate at all (nil, so the UI can say "—" instead of "0%").
func TestBuildIndexerStatDerivesRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		snap     statSnapshot
		wantAvg  int64
		wantRate *float64
	}{
		{name: "no traffic at all", snap: statSnapshot{}},
		{
			name:    "average over queries",
			snap:    statSnapshot{queries: 4, respTotal: 1000},
			wantAvg: 250,
		},
		{
			name:     "rate over attempts",
			snap:     statSnapshot{grabAttempts: 5, grabs: 4},
			wantRate: ptrFloat(0.8),
		},
		{
			name:     "every attempt failed",
			snap:     statSnapshot{grabAttempts: 3},
			wantRate: ptrFloat(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildIndexerStat("s", tt.snap, database.HealthCounts{}, nil)
			if got.AvgResponseMs != tt.wantAvg {
				t.Errorf("avgResponseMs = %d, want %d", got.AvgResponseMs, tt.wantAvg)
			}
			switch {
			case tt.wantRate == nil && got.GrabSuccessRate != nil:
				t.Errorf("grabSuccessRate = %v, want nil (no attempts)", *got.GrabSuccessRate)
			case tt.wantRate != nil && got.GrabSuccessRate == nil:
				t.Errorf("grabSuccessRate = nil, want %v", *tt.wantRate)
			case tt.wantRate != nil && *got.GrabSuccessRate != *tt.wantRate:
				t.Errorf("grabSuccessRate = %v, want %v", *got.GrabSuccessRate, *tt.wantRate)
			}
		})
	}
}

func ptrFloat(f float64) *float64 { return &f }

// TestCategoryName proves the id-0 bucket reads as "Uncategorized" and never collides
// with the real "Other" family (8000).
func TestCategoryName(t *testing.T) {
	t.Parallel()

	for id, want := range map[int]string{0: "Uncategorized", 2000: "Movies", 8000: "Other", 12345: "Uncategorized"} {
		if got := categoryName(id); got != want {
			t.Errorf("categoryName(%d) = %q, want %q", id, got, want)
		}
	}
}
