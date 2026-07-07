package dateparse_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// fixedClock returns a deterministic clock so missing-year and relative-time math
// are reproducible across runs and platforms (the Windows CI runner included).
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2024, time.June, 1, 12, 0, 0, 0, time.UTC) }
}

func TestTranslateLayoutTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		net    string
		goWant string
	}{
		{"year-4", "yyyy", "2006"},
		{"year-2", "yy", "06"},
		{"month-full", "MMMM", "January"},
		{"month-abbr", "MMM", "Jan"},
		{"month-pad", "MM", "01"},
		{"month-bare", "M", "1"},
		{"day-full", "dddd", "Monday"},
		{"day-abbr", "ddd", "Mon"},
		{"day-pad", "dd", "02"},
		{"day-bare", "d", "2"},
		{"hour-24-pad", "HH", "15"},
		{"hour-24-bare", "H", "15"},
		{"hour-12-pad", "hh", "03"},
		{"hour-12-bare", "h", "3"},
		{"minute-pad", "mm", "04"},
		{"minute-bare", "m", "4"},
		{"second-pad", "ss", "05"},
		{"second-bare", "s", "5"},
		{"ampm-double", "tt", "PM"},
		{"ampm-single", "t", "PM"},
		{"tz-zzz", "zzz", "-07:00"},
		{"tz-zz", "zz", "-07"},
		{"tz-K", "K", "Z07:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := dateparse.TranslateLayout(c.net)
			if err != nil {
				t.Fatalf("TranslateLayout(%q) error: %v", c.net, err)
			}
			if got != c.goWant {
				t.Fatalf("TranslateLayout(%q) = %q, want %q", c.net, got, c.goWant)
			}
		})
	}
}

func TestTranslateLayoutGreedyAndQuirks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		net    string
		goWant string
	}{
		{"yyyy-MM-dd HH:mm:ss zzz", "2006-01-02 15:04:05 -07:00"},
		{"yyyy-MM-ddHH:mm:ss zzz", "2006-01-0215:04:05 -07:00"}, // no-space quirk
		{"MM/dd/yyyy HH:mm:ss zzz", "01/02/2006 15:04:05 -07:00"},
		{"ddd, dd MMM yyyy HH:mm:ss zzz", "Mon, 02 Jan 2006 15:04:05 -07:00"},
		{"dddd MMMM d yyyy", "Monday January 2 2006"},
		{"d.MMMyyyyHH:mm", "2.Jan200615:04"}, // MMM abutting digits + no-space
		{"htt MMM. d", "3PM Jan. 2"},
		{"yy/MM/dd", "06/01/02"},
		{"H:mm", "15:04"},
		{"d MMM yy HH:mm:ss zzz", "2 Jan 06 15:04:05 -07:00"},
		// Fractional seconds: the literal '.'/',' separator collapses into Go's
		// fractional layout (no doubled separator like "05..000").
		{"HH:mm:ss.fff", "15:04:05.000"},
		{"ss.FFF", "05.999"},
		{"ss,ff", "05,00"},
	}
	for _, c := range cases {
		t.Run(c.net, func(t *testing.T) {
			t.Parallel()
			got, err := dateparse.TranslateLayout(c.net)
			if err != nil {
				t.Fatalf("TranslateLayout(%q) error: %v", c.net, err)
			}
			if got != c.goWant {
				t.Fatalf("TranslateLayout(%q) = %q, want %q", c.net, got, c.goWant)
			}
		})
	}
}

func TestTranslateLayoutUnknownToken(t *testing.T) {
	t.Parallel()
	// 'Q' (quarter) is not a supported token; it is a format letter only in some
	// cultures and Go has no equivalent — must error, not silently pass through.
	if _, err := dateparse.TranslateLayout("yyyy-Q"); err == nil {
		t.Fatal("expected error for unknown token 'Q', got nil")
	}
}

func TestTranslateLayoutBareFractionalUnsupported(t *testing.T) {
	t.Parallel()
	// A fractional token with no preceding '.'/',' separator has no Go equivalent
	// (Go cannot render fractional seconds without a separator) — must error loudly
	// rather than invent a spurious dot.
	if _, err := dateparse.TranslateLayout("ssfff"); err == nil {
		t.Fatal("expected error for bare fractional token 'fff' without a separator, got nil")
	}
}

func TestTranslateLayoutSingleZUnsupported(t *testing.T) {
	t.Parallel()
	// .NET single `z` (sign + variable-width hours) has no Go equivalent that
	// parses real "+02" values; rather than emit a silently-broken "-7" mapping
	// it must error loudly. `zz`/`zzz` remain supported. No corpus format uses a
	// bare `z`, so this is a latent-correctness guard, not a live behavior.
	if _, err := dateparse.TranslateLayout("HH:mm z"); err == nil {
		t.Fatal("expected error for unsupported single 'z' token, got nil")
	}
	if _, err := dateparse.TranslateLayout("zz"); err != nil {
		t.Fatalf("TranslateLayout(%q) should still succeed: %v", "zz", err)
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	p := dateparse.New(dateparse.WithClock(fixedClock()))
	cases := []struct {
		name   string
		layout string
		value  string
		want   string
	}{
		{"iso-tz", "yyyy-MM-dd HH:mm:ss zzz", "2023-01-02 15:04:05 +02:00", "2023-01-02T15:04:05+02:00"},
		{"no-space-quirk", "yyyy-MM-ddHH:mm:ss zzz", "2023-01-0215:04:05 +02:00", "2023-01-02T15:04:05+02:00"},
		{"us-slash", "MM/dd/yyyy HH:mm:ss zzz", "01/02/2023 15:04:05 -05:00", "2023-01-02T15:04:05-05:00"},
		{"rfc1123z-shape", "ddd, dd MMM yyyy HH:mm:ss zzz", "Mon, 02 Jan 2023 15:04:05 +00:00", "2023-01-02T15:04:05Z"},
		{"12h-pm", "MMM d yyyy hh:mm tt", "Jan 2 2023 03:04 PM", "2023-01-02T15:04:00Z"},
		{"12h-am", "MMM d yyyy hh:mm tt", "Jan 2 2023 03:04 AM", "2023-01-02T03:04:00Z"},
		{"24h-single", "yyyy-M-d H:m:s", "2023-1-2 5:4:3", "2023-01-02T05:04:03Z"},
		{"full-month", "d MMMM yyyy HH:mm:ss zzz", "2 January 2023 15:04:05 +02:00", "2023-01-02T15:04:05+02:00"},
		{"two-digit-year", "yy-MM-dd hh:mm:ss tt", "23-01-02 03:04:05 PM", "2023-01-02T15:04:05Z"},
		{"tz-zz", "yyyy-MM-dd HH:mm:ss zz", "2023-01-02 15:04:05 +02", "2023-01-02T15:04:05+02:00"},
		{"missing-year-defaults-clock", "MM/dd HH:mm", "03/15 09:30", "2024-03-15T09:30:00Z"},
		// Yearless layouts containing y/h/d hit Jackett's commonStandardFormats
		// early return, so a FUTURE result is NOT rolled back (12-25 is after the
		// 2024-06-01 clock but stays 2024). This pins parity against reintroducing
		// an ungated rollback (see rollbackFutureYearless).
		{"missing-year-month-day", "MM-dd", "12-25", "2024-12-25T00:00:00Z"},
		// .NET matches AM/PM designators case-insensitively (Jackett parses these);
		// normalizeAMPM compensates for Go's uppercase-only PM token.
		{"lowercase-pm", "yy-MM-dd hh:mm:ss tt", "23-01-02 03:04:05 pm", "2023-01-02T15:04:05Z"},
		{"lowercase-am", "MMM d yyyy hh:mm tt", "Jan 2 2023 03:04 am", "2023-01-02T03:04:00Z"},
		{"mixed-case-pm", "MMM d yyyy hh:mm tt", "Jan 2 2023 03:04 Pm", "2023-01-02T15:04:00Z"},
		// 1337x's "htt MMM. d": designator attached to the hour digit, no year.
		{"lowercase-pm-attached", "htt MMM. d", "3pm Jan. 2", "2024-01-02T15:00:00Z"},
		// mixtapetorrent's "MMM d yyyy - h:mmtt zzz": designator attached to the
		// minutes, followed by a zone.
		{"lowercase-pm-attached-zone", "MMM d yyyy - h:mmtt zzz", "Jan 2 2023 - 3:04pm +02:00", "2023-01-02T15:04:00+02:00"},
		// abtorrents' "MMM d yyyyh:mm tt": year and hour abut with no separator.
		{"lowercase-pm-abutting-year", "MMM d yyyyh:mm tt", "Jan 2 20233:04 pm", "2023-01-02T15:04:00Z"},
		// Time-only layouts take the FULL date from the clock (.NET DateTimeParse
		// defaults, not Go's January 1): torrentqq's "HH:mm" and comicat's
		// timeparse "HH:mm zzz" ("今天 14:30" -> replace/append -> "14:30 +08:00").
		{"time-only-defaults-today", "HH:mm", "14:30", "2024-06-01T14:30:00Z"},
		{"time-only-with-zone", "HH:mm zzz", "14:30 +08:00", "2024-06-01T14:30:00+08:00"},
		// A weekday NAME is not a date component (.NET and Go alike): a layout with
		// only a weekday + time still defaults the full date from the clock.
		{"weekday-name-is-not-a-date", "ddd HH:mm", "Sat 14:30", "2024-06-01T14:30:00Z"},
		// Letterless yearless layouts (no y/h/d) DO reach Jackett's rollback tail
		// (ParseDateTimeGoLangTest): a future result rolls back one year, a past
		// result does not. Dormant for the corpus (no such layout exists today).
		{"letterless-future-rolls-back", "MMM", "Dec", "2023-12-01T00:00:00Z"},
		{"letterless-past-no-rollback", "MMM", "Jan", "2024-01-01T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := p.ParseDate(c.value, c.layout)
			if err != nil {
				t.Fatalf("ParseDate(%q, %q) error: %v", c.value, c.layout, err)
			}
			if got != c.want {
				t.Fatalf("ParseDate(%q, %q) = %q, want %q", c.value, c.layout, got, c.want)
			}
		})
	}
}

func TestParseDateLocalizedNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lang   string
		layout string
		value  string
		want   string
	}{
		{"ru-RU", "d MMMM yyyy HH:mm:ss zzz", "2 января 2023 15:04:05 +02:00", "2023-01-02T15:04:05+02:00"},
		{"ru-RU", "d MMM yyyy", "2 фев 2023", "2023-02-02T00:00:00Z"},
		{"de-DE", "d MMMM yyyy", "2 März 2023", "2023-03-02T00:00:00Z"},
		{"de-DE", "ddd, d MMM yyyy", "Mo, 2 Jan 2023", "2023-01-02T00:00:00Z"},
		{"fr-FR", "d MMMM yyyy", "2 février 2023", "2023-02-02T00:00:00Z"},
		{"es-ES", "d MMMM yyyy", "2 diciembre 2023", "2023-12-02T00:00:00Z"},
		{"it-IT", "d MMMM yyyy", "2 marzo 2023", "2023-03-02T00:00:00Z"},
		{"el-GR", "d MMMM yyyy", "2 Μαρτίου 2023", "2023-03-02T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.lang+"/"+c.value, func(t *testing.T) {
			t.Parallel()
			p := dateparse.New(dateparse.WithClock(fixedClock()), dateparse.WithLanguage(c.lang))
			got, err := p.ParseDate(c.value, c.layout)
			if err != nil {
				t.Fatalf("ParseDate(%q, %q) lang %q error: %v", c.value, c.layout, c.lang, err)
			}
			if got != c.want {
				t.Fatalf("ParseDate(%q, %q) lang %q = %q, want %q", c.value, c.layout, c.lang, got, c.want)
			}
		})
	}
}

func TestParseDateFailureSurfaces(t *testing.T) {
	t.Parallel()
	p := dateparse.New(dateparse.WithClock(fixedClock()))
	// Value does not match layout: Jackett throws/logs; we must surface an error,
	// never silently pass the raw value through.
	if _, err := p.ParseDate("not a date", "yyyy-MM-dd"); err == nil {
		t.Fatal("expected error for mismatched value, got nil")
	}
}

func TestParseRelTime(t *testing.T) {
	t.Parallel()
	p := dateparse.New(dateparse.WithClock(fixedClock()))
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"minutes-ago", "5 minutes ago", "2024-06-01T11:55:00Z"},
		{"minute-singular", "1 minute ago", "2024-06-01T11:59:00Z"},
		{"hours-ago", "2 hours ago", "2024-06-01T10:00:00Z"},
		{"hr-abbr", "3 hr ago", "2024-06-01T09:00:00Z"},
		{"days-ago", "1 day ago", "2024-05-31T12:00:00Z"},
		{"weeks-ago", "2 weeks ago", "2024-05-18T12:00:00Z"},
		{"wk-abbr", "1 wk ago", "2024-05-25T12:00:00Z"},
		{"compound", "1 day 2 hours ago", "2024-05-31T10:00:00Z"},
		{"seconds-abbr", "30 sec ago", "2024-06-01T11:59:30Z"},
		{"just-now", "just now", "2024-06-01T12:00:00Z"},
		{"now", "now", "2024-06-01T12:00:00Z"},
		{"yesterday-time", "yesterday 14:22", "2024-05-31T14:22:00Z"},
		{"today-time", "today 08:00", "2024-06-01T08:00:00Z"},
		{"tomorrow", "tomorrow", "2024-06-02T00:00:00Z"},
		{"unix-seconds", "1577880000", "2020-01-01T12:00:00Z"},
		// PARITY: Jackett treats EVERY all-digit value as seconds (no millis
		// heuristic), so a 13-digit value renders as a far-future seconds
		// timestamp rather than a 2020 millisecond epoch.
		{"unix-13digit-as-seconds", "1577880000000", "51971-01-11T00:00:00Z"},
		{"iso-8601", "2023-01-02T15:04:05Z", "2023-01-02T15:04:05Z"},
		{"iso-space", "2023-01-02 15:04:05", "2023-01-02T15:04:05Z"},
		{"iso-space-no-secs", "2023-01-02 15:04", "2023-01-02T15:04:00Z"},
		// Human absolute formats Jackett reaches via DateTime.Parse(InvariantCulture).
		{"month-abbr-comma", "Jan 15, 2023", "2023-01-15T00:00:00Z"},
		{"month-full-comma", "January 15, 2023", "2023-01-15T00:00:00Z"},
		{"day-month-abbr", "15 Aug 2023", "2023-08-15T00:00:00Z"},
		{"slash-ymd", "2023/08/15", "2023-08-15T00:00:00Z"},
		{"slash-mdy-invariant", "08/15/2023", "2023-08-15T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := p.ParseRelTime(c.value)
			if err != nil {
				t.Fatalf("ParseRelTime(%q) error: %v", c.value, err)
			}
			if got != c.want {
				t.Fatalf("ParseRelTime(%q) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}

func TestParseRelTimeLocalized(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lang  string
		value string
		want  string
	}{
		{"ru-RU", "2 часа назад", "2024-06-01T10:00:00Z"},
		{"ru-RU", "5 минут назад", "2024-06-01T11:55:00Z"},
		{"ru-RU", "Вчера", "2024-05-31T00:00:00Z"},
		{"ru-RU", "Сегодня", "2024-06-01T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.lang+"/"+c.value, func(t *testing.T) {
			t.Parallel()
			p := dateparse.New(dateparse.WithClock(fixedClock()), dateparse.WithLanguage(c.lang))
			got, err := p.ParseRelTime(c.value)
			if err != nil {
				t.Fatalf("ParseRelTime(%q) lang %q error: %v", c.value, c.lang, err)
			}
			if got != c.want {
				t.Fatalf("ParseRelTime(%q) lang %q = %q, want %q", c.value, c.lang, got, c.want)
			}
		})
	}
}

func TestParseRelTimeFailure(t *testing.T) {
	t.Parallel()
	p := dateparse.New(dateparse.WithClock(fixedClock()))
	if _, err := p.ParseRelTime("gibberish"); err == nil {
		t.Fatal("expected error for unparseable relative value, got nil")
	}
}

// representativeValue produces a value string by formatting a fixed sample
// instant through the translated Go layout. The census parses it back to confirm
// the translated layout is INTERNALLY CONSISTENT (Go can both format and parse
// it).
//
// LIMITATION: because format and parse use the same translated layout, this
// round-trip is self-fulfilling — it proves "translates and self-round-trips",
// NOT "parses real Jackett feed values". A layout that translates to a Go layout
// Go cannot parse against real input (the historic single-`z` -> "-7" case) would
// still pass here. Real-value parity is the job of TestParseDate /
// TestParseDateLocalizedNames (hand-written feed values); the census is a
// translate-coverage gate over the whole corpus.
func representativeValue(netLayout string) string {
	// Build by translating then formatting the fixed sample instant through the
	// Go layout; this guarantees a parseable value for any translatable layout.
	goLayout, err := dateparse.TranslateLayout(netLayout)
	if err != nil {
		return ""
	}
	sample := time.Date(2023, time.January, 2, 15, 4, 5, 0, time.FixedZone("", 2*3600))
	return sample.Format(goLayout)
}

// TestCorpusCensus is the translate-coverage gate: it loads the embedded Jackett
// definition snapshot, collects every distinct dateparse/timeparse format-string
// arg, and asserts each one translates without error and self-round-trips a
// representative value through ParseDate. Any untranslatable format fails loudly
// (never silently skipped). Per-format counts are reported via t.Logf.
//
// This is a COVERAGE gate, not the real-value parity gate (see
// representativeValue's limitation note) — TestParseDate /
// TestParseDateLocalizedNames assert hand-written feed values for parity.
func TestCorpusCensus(t *testing.T) {
	t.Parallel()
	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("loader LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatalf("no definitions loaded (skipped %d)", len(skipped))
	}

	counts := collectDateFormats(defs)
	if len(counts) == 0 {
		t.Fatal("census found no dateparse/timeparse formats — corpus traversal is broken")
	}

	formats := make([]string, 0, len(counts))
	for f := range counts {
		formats = append(formats, f)
	}
	sort.Slice(formats, func(i, j int) bool {
		if counts[formats[i]] != counts[formats[j]] {
			return counts[formats[i]] > counts[formats[j]]
		}
		return formats[i] < formats[j]
	})

	p := dateparse.New(dateparse.WithClock(fixedClock()))
	var failures []string
	for _, f := range formats {
		t.Logf("%4d  %q", counts[f], f)
		if _, err := dateparse.TranslateLayout(f); err != nil {
			if _, known := knownUnsupported[f]; known {
				continue
			}
			failures = append(failures, f+" (translate: "+err.Error()+")")
			continue
		}
		val := representativeValue(f)
		if val == "" {
			failures = append(failures, f+" (no representative value)")
			continue
		}
		if _, err := p.ParseDate(val, f); err != nil {
			if _, known := knownUnsupported[f]; known {
				continue
			}
			failures = append(failures, f+" (parse "+val+": "+err.Error()+")")
		}
	}

	t.Logf("census: %d distinct formats across the corpus", len(formats))
	if len(failures) > 0 {
		t.Fatalf("%d corpus format(s) failed the census:\n%s",
			len(failures), strings.Join(failures, "\n"))
	}
}

// knownUnsupported is the explicit, surfaced baseline of corpus formats we cannot
// yet handle. It is empty: every distinct corpus dateparse/timeparse format
// translates and round-trips. An entry here is a loud, reviewed exception, never
// a silent skip.
var knownUnsupported = map[string]struct{}{}

// collectDateFormats walks every loaded definition and tallies the format arg of
// each dateparse/timeparse field filter.
func collectDateFormats(defs []*loader.Definition) map[string]int {
	counts := map[string]int{}
	for _, def := range defs {
		for _, fe := range def.Search.Fields.Ordered() {
			tallyFilters(counts, fe.Block.Filters)
		}
	}
	return counts
}

// tallyFilters increments counts for each dateparse/timeparse filter's first arg.
func tallyFilters(counts map[string]int, filters []loader.FilterBlock) {
	for _, f := range filters {
		if f.Name != "dateparse" && f.Name != "timeparse" {
			continue
		}
		if len(f.Args) == 0 {
			continue
		}
		counts[f.Args[0]]++
	}
}
