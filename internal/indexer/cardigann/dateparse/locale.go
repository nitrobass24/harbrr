package dateparse

import (
	"strings"
	"unicode"
)

// locale holds the localized month and weekday names for one language, in both
// full (MMMM/dddd) and abbreviated (MMM/ddd) forms. Indices are calendar order:
// months[0]=January, days[0]=Monday — matching Go's time.Month/Weekday ordering
// offset (we normalize day order to Monday-first internally).
//
// PARITY NOTE: Jackett's dateparse/timeparse filter parses with
// CultureInfo.InvariantCulture (English names only); localized month/day names in
// the corpus are pre-normalized to English by `replace` filters upstream. This
// table is therefore an OPT-IN enhancement enabled by WithLanguage — the default
// (no language) parser uses Go's English names and matches Jackett byte-for-byte.
// When a language is set we substitute localized names <-> English around
// time.Parse, because Go's time package only knows English names.
type locale struct {
	monthsFull []string // 12 entries, January..December
	monthsAbbr []string // 12 entries
	daysFull   []string // 7 entries, Monday..Sunday
	daysAbbr   []string // 7 entries
}

// englishMonthsFull/Abbr and englishDays* are the targets we translate INTO so
// Go's time.Parse (English-only) can consume the value.
var (
	englishMonthsFull = []string{
		"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December",
	}
	englishMonthsAbbr = []string{
		"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	}
	englishDaysFull = []string{
		"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday",
	}
	englishDaysAbbr = []string{
		"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun",
	}
)

// locales maps a CultureInfo-style language code (lowercased, e.g. "ru-ru") to
// its name table. Lookup also accepts the bare primary subtag ("ru"). Names are
// drawn from CLDR/.NET CultureInfo for the locales the corpus actually uses for
// MMM/MMMM layouts: ru, de, fr, es, it, el, pt, sl.
var locales = map[string]locale{
	"ru": {
		monthsFull: []string{
			"января", "февраля", "марта", "апреля", "мая", "июня",
			"июля", "августа", "сентября", "октября", "ноября", "декабря",
		},
		monthsAbbr: []string{
			"янв", "фев", "мар", "апр", "май", "июн",
			"июл", "авг", "сен", "окт", "ноя", "дек",
		},
		daysFull: []string{
			"понедельник", "вторник", "среда", "четверг", "пятница", "суббота", "воскресенье",
		},
		daysAbbr: []string{"пн", "вт", "ср", "чт", "пт", "сб", "вс"},
	},
	"de": {
		monthsFull: []string{
			"Januar", "Februar", "März", "April", "Mai", "Juni",
			"Juli", "August", "September", "Oktober", "November", "Dezember",
		},
		monthsAbbr: []string{
			"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
			"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
		},
		daysFull: []string{
			"Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag",
		},
		daysAbbr: []string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"},
	},
	"fr": {
		monthsFull: []string{
			"janvier", "février", "mars", "avril", "mai", "juin",
			"juillet", "août", "septembre", "octobre", "novembre", "décembre",
		},
		monthsAbbr: []string{
			"janv", "févr", "mars", "avr", "mai", "juin",
			"juil", "août", "sept", "oct", "nov", "déc",
		},
		daysFull: []string{
			"lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi", "dimanche",
		},
		daysAbbr: []string{"lun", "mar", "mer", "jeu", "ven", "sam", "dim"},
	},
	"es": {
		monthsFull: []string{
			"enero", "febrero", "marzo", "abril", "mayo", "junio",
			"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
		},
		monthsAbbr: []string{
			"ene", "feb", "mar", "abr", "may", "jun",
			"jul", "ago", "sep", "oct", "nov", "dic",
		},
		daysFull: []string{
			"lunes", "martes", "miércoles", "jueves", "viernes", "sábado", "domingo",
		},
		daysAbbr: []string{"lun", "mar", "mié", "jue", "vie", "sáb", "dom"},
	},
	"it": {
		monthsFull: []string{
			"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno",
			"luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre",
		},
		monthsAbbr: []string{
			"gen", "feb", "mar", "apr", "mag", "giu",
			"lug", "ago", "set", "ott", "nov", "dic",
		},
		daysFull: []string{
			"lunedì", "martedì", "mercoledì", "giovedì", "venerdì", "sabato", "domenica",
		},
		daysAbbr: []string{"lun", "mar", "mer", "gio", "ven", "sab", "dom"},
	},
	"el": {
		monthsFull: []string{
			"Ιανουαρίου", "Φεβρουαρίου", "Μαρτίου", "Απριλίου", "Μαΐου", "Ιουνίου",
			"Ιουλίου", "Αυγούστου", "Σεπτεμβρίου", "Οκτωβρίου", "Νοεμβρίου", "Δεκεμβρίου",
		},
		monthsAbbr: []string{
			"Ιαν", "Φεβ", "Μαρ", "Απρ", "Μαϊ", "Ιουν",
			"Ιουλ", "Αυγ", "Σεπ", "Οκτ", "Νοε", "Δεκ",
		},
		daysFull: []string{
			"Δευτέρα", "Τρίτη", "Τετάρτη", "Πέμπτη", "Παρασκευή", "Σάββατο", "Κυριακή",
		},
		daysAbbr: []string{"Δευ", "Τρι", "Τετ", "Πεμ", "Παρ", "Σαβ", "Κυρ"},
	},
	"pt": {
		monthsFull: []string{
			"janeiro", "fevereiro", "março", "abril", "maio", "junho",
			"julho", "agosto", "setembro", "outubro", "novembro", "dezembro",
		},
		monthsAbbr: []string{
			"jan", "fev", "mar", "abr", "mai", "jun",
			"jul", "ago", "set", "out", "nov", "dez",
		},
		daysFull: []string{
			"segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado", "domingo",
		},
		daysAbbr: []string{"seg", "ter", "qua", "qui", "sex", "sáb", "dom"},
	},
	"sl": {
		monthsFull: []string{
			"januar", "februar", "marec", "april", "maj", "junij",
			"julij", "avgust", "september", "oktober", "november", "december",
		},
		monthsAbbr: []string{
			"jan", "feb", "mar", "apr", "maj", "jun",
			"jul", "avg", "sep", "okt", "nov", "dec",
		},
		daysFull: []string{
			"ponedeljek", "torek", "sreda", "četrtek", "petek", "sobota", "nedelja",
		},
		daysAbbr: []string{"pon", "tor", "sre", "čet", "pet", "sob", "ned"},
	},
}

// lookupLocale resolves a CultureInfo-style code ("ru-RU", "de-DE", "pt-BR") to a
// name table, trying the full lowercased code then the primary subtag. The
// boolean is false when no table exists (English fallback).
func lookupLocale(lang string) (locale, bool) {
	if lang == "" {
		return locale{}, false
	}
	key := strings.ToLower(lang)
	if loc, ok := locales[key]; ok {
		return loc, true
	}
	if i := strings.IndexByte(key, '-'); i > 0 {
		if loc, ok := locales[key[:i]]; ok {
			return loc, true
		}
	}
	return locale{}, false
}

// localizeValue rewrites localized month/day names in value to their English
// equivalents so Go's time.Parse can consume them.
//
// It tokenizes value into alphabetic words and non-word separators, then looks
// each WORD up in a localized->English map. Whole-word matching is essential:
// substring replacement collides (e.g. Italian month "marzo"->"March" then the
// Italian Tuesday abbr "mar" would corrupt "March"->"Tuech"). Substituted English
// words are emitted verbatim and never re-scanned. Longest source names register
// first so an abbreviation never shadows a full name. Matching is
// case-insensitive; unknown words pass through for time.Parse to reject loudly.
func localizeValue(value string, loc locale) string {
	table := loc.lookupTable()
	var b strings.Builder
	b.Grow(len(value))
	for _, run := range splitWords(value) {
		if run.isWord {
			if eng, ok := table[strings.ToLower(run.text)]; ok {
				b.WriteString(eng)
				continue
			}
		}
		b.WriteString(run.text)
	}
	return b.String()
}

// wordRun is a maximal run of either letters (isWord) or non-letters.
type wordRun struct {
	text   string
	isWord bool
}

// splitWords partitions s into alternating word / non-word runs, preserving every
// byte so the value can be reassembled losslessly. A hyphen counts as a word rune
// so hyphenated locale names (e.g. Portuguese "segunda-feira") stay a single run
// and match their lookupTable key instead of splitting into "segunda"/"feira".
func splitWords(s string) []wordRun {
	var runs []wordRun
	var cur strings.Builder
	var curWord bool
	flush := func() {
		if cur.Len() > 0 {
			runs = append(runs, wordRun{text: cur.String(), isWord: curWord})
			cur.Reset()
		}
	}
	for _, r := range s {
		isW := isWordRune(r)
		if cur.Len() > 0 && isW != curWord {
			flush()
		}
		curWord = isW
		cur.WriteRune(r)
	}
	flush()
	return runs
}

// isWordRune reports whether r is part of a locale name word. Letters qualify, as
// do hyphen runes (ASCII '-' and the Unicode hyphen/non-breaking hyphen) so
// hyphenated weekday/month names tokenize as one word.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || r == '-' || r == '‐' || r == '‑'
}

// lookupTable builds the localized(lowercased)->English name map for this locale.
// Full names register before abbreviated so the longer, more specific form wins
// on collision; days register after months because month-name parsing dominates
// the corpus and any overlap should resolve to the month.
func (l locale) lookupTable() map[string]string {
	table := make(map[string]string, 38)
	addNames(table, l.daysAbbr, englishDaysAbbr)
	addNames(table, l.monthsAbbr, englishMonthsAbbr)
	addNames(table, l.daysFull, englishDaysFull)
	addNames(table, l.monthsFull, englishMonthsFull)
	return table
}

// addNames registers each src[i]->dst[i] pair (lowercased key) into table.
func addNames(table map[string]string, src, dst []string) {
	for i, name := range src {
		if name == "" || i >= len(dst) {
			continue
		}
		table[strings.ToLower(name)] = dst[i]
	}
}
