package template

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
			if want := strconv.Itoa(tc.clock.Day()); got.Day != want {
				t.Errorf("Day = %q, want %q (unaffected by the year quirk)", got.Day, want)
			}
		})
	}
}

// TestNewSeededTodayUnsetWithoutClock pins that a nil Clock leaves .Today at
// its zero value rather than defaulting to time.Now — the behavior login's
// templateContext relies on, since it never renders .Today.
func TestNewSeededTodayUnsetWithoutClock(t *testing.T) {
	t.Parallel()

	ctx := NewSeeded(Params{Config: map[string]string{"username": "alice"}, BaseURL: "https://example.org"})
	want := Today{}
	if ctx.Today != want {
		t.Errorf("Today = %+v, want zero value %+v", ctx.Today, want)
	}
}

// TestNewSeededTodayFromClock pins that a supplied Clock seeds .Today via the
// same January-rollover quirk as today().
func TestNewSeededTodayFromClock(t *testing.T) {
	t.Parallel()

	clock := func() time.Time { return time.Date(2024, time.January, 3, 0, 0, 0, 0, time.UTC) }
	ctx := NewSeeded(Params{Clock: clock})
	want := Today{Year: "2023", Month: "1", Day: "3"}
	if ctx.Today != want {
		t.Errorf("Today = %+v, want %+v", ctx.Today, want)
	}
}

// TestNewSeededSitelinkDefault pins NewSeeded's .Config.sitelink defaulting:
// an unset sitelink is filled from BaseURL; an explicitly set sitelink is
// preserved untouched, matching Jackett's GetBaseTemplateVariables seeding.
func TestNewSeededSitelinkDefault(t *testing.T) {
	t.Parallel()

	t.Run("unset sitelink defaults from BaseURL", func(t *testing.T) {
		t.Parallel()
		ctx := NewSeeded(Params{Config: map[string]string{"username": "alice"}, BaseURL: "https://example.org"})
		if got := ctx.Config["sitelink"]; got != "https://example.org" {
			t.Errorf("Config[sitelink] = %q, want %q", got, "https://example.org")
		}
	})

	t.Run("set sitelink is preserved", func(t *testing.T) {
		t.Parallel()
		ctx := NewSeeded(Params{
			Config:  map[string]string{"sitelink": "https://mirror.example.org"},
			BaseURL: "https://example.org",
		})
		if got := ctx.Config["sitelink"]; got != "https://mirror.example.org" {
			t.Errorf("Config[sitelink] = %q, want %q (caller-set value preserved)", got, "https://mirror.example.org")
		}
	})

	t.Run("input Config map is not mutated", func(t *testing.T) {
		t.Parallel()
		in := map[string]string{"username": "alice"}
		_ = NewSeeded(Params{Config: in, BaseURL: "https://example.org"})
		if _, ok := in["sitelink"]; ok {
			t.Errorf("caller's Config map was mutated with a sitelink key")
		}
	})
}
