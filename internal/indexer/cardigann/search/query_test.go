package search

import (
	"strconv"
	"testing"
	"time"
)

// TestTodayYearQuirk pins Jackett's .Today.Year quirk: January reports the
// previous year (Month > 1 ? Year : Year - 1), every other month reports the
// current year. Month and Day are unaffected by the quirk.
func TestTodayYearQuirk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		clock    time.Time
		wantYear string
	}{
		{"january reports previous year", time.Date(2023, time.January, 15, 0, 0, 0, 0, time.UTC), "2022"},
		{"february reports current year", time.Date(2023, time.February, 1, 0, 0, 0, 0, time.UTC), "2023"},
		{"december reports current year", time.Date(2023, time.December, 31, 0, 0, 0, 0, time.UTC), "2023"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := today(func() time.Time { return tc.clock })
			if got.Year != tc.wantYear {
				t.Errorf("Year = %q, want %q", got.Year, tc.wantYear)
			}
			if want := strconv.Itoa(int(tc.clock.Month())); got.Month != want {
				t.Errorf("Month = %q, want %q (unaffected by the year quirk)", got.Month, want)
			}
		})
	}
}
