package regexadapter

import "strings"

// nonLatinPrimaries maps a BCP-47 primary language subtag to its dominant
// non-Latin script. Cardigann def `language:` codes are region-qualified
// (e.g. "zh-CN", "ru-RU", "el-GR"); only the primary subtag determines script.
//
// Coverage spans the corpus census (zh, ru, el, th, ar, uk, ko, he) plus the
// common remainder of non-Latin-script languages so a future def does not
// silently fall back to RE2 for a script RE2 mishandles (case-folding, \w word
// boundaries). Languages written in Latin script (en, de, fr, es, it, pt, nl,
// pl, cs, tr, vi, id, ...) are intentionally absent — they route to RE2.
var nonLatinPrimaries = map[string]struct{}{
	// CJK
	"zh": {}, // Chinese (Han)
	"ja": {}, // Japanese (Kanji/Kana)
	"ko": {}, // Korean (Hangul)
	// Cyrillic
	"ru": {}, // Russian
	"uk": {}, // Ukrainian
	"be": {}, // Belarusian
	"bg": {}, // Bulgarian
	"sr": {}, // Serbian (Cyrillic)
	"mk": {}, // Macedonian
	"kk": {}, // Kazakh
	"mn": {}, // Mongolian (Cyrillic)
	// Greek
	"el": {},
	// Semitic / RTL
	"ar": {}, // Arabic
	"he": {}, // Hebrew
	"iw": {}, // Hebrew (legacy code)
	"fa": {}, // Persian (Arabic script)
	"ur": {}, // Urdu (Arabic script)
	// Brahmic
	"th": {}, // Thai
	"hi": {}, // Hindi (Devanagari)
	"bn": {}, // Bengali
	"ta": {}, // Tamil
	"te": {}, // Telugu
	"kn": {}, // Kannada
	"ml": {}, // Malayalam
	"si": {}, // Sinhala
	"my": {}, // Burmese
	"km": {}, // Khmer
	"lo": {}, // Lao
	// Caucasus
	"ka": {}, // Georgian
	"hy": {}, // Armenian
	// Other
	"am": {}, // Amharic (Ge'ez)
}

// isNonLatinScript reports whether a Cardigann `language:` code denotes a
// language whose dominant script is non-Latin, which forces the regexp2 route.
// The check is on the primary subtag, lowercased; an empty or unknown code is
// treated as Latin (RE2 default).
func isNonLatinScript(lang string) bool {
	primary := primarySubtag(lang)
	if primary == "" {
		return false
	}
	_, ok := nonLatinPrimaries[primary]
	return ok
}

// primarySubtag extracts and lowercases the BCP-47 primary language subtag
// (the part before the first '-' or '_').
func primarySubtag(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ""
	}
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	return strings.ToLower(lang)
}
