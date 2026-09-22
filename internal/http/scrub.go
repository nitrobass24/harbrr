package http

import (
	"slices"
	"sort"
	"strings"
)

// scrubValuePlaceholder is the placeholder a value-scrubbed credential is replaced
// with. It is DELIBERATELY distinct from redactedValue ("REDACTED", used by the
// name-matched URL/header/JSON surfaces above): ScrubValues scrubs a caller-supplied
// VALUE out of free text (a server-echoed credential), not a name-matched field, and
// every existing call site (the former cardigann/login primitive, and every native
// driver's hand-rolled scrub) already committed to this exact spelling in tests and
// persisted health-event details.
const scrubValuePlaceholder = "[redacted]"

// ScrubValues returns s with every non-empty value in values replaced by the
// redaction placeholder, longest value first. It is the shared value-scrub
// primitive: the caller derives which config values are secret (see
// loader.SecretValues) and hands the raw VALUES here — this package stays a pure
// leaf and never learns what a "secret setting" is.
//
// Longest-first ordering is substring safety: if one secret is a substring of
// another (e.g. an ApiUser contained in a longer ApiKey), replacing the shorter one
// first would mangle or partially miss the longer one, leaking a fragment. Empty
// values are skipped so strings.ReplaceAll is never handed "" (which would splice
// the placeholder between every rune of s).
func ScrubValues(s string, values []string) string {
	if len(values) == 0 {
		return s
	}
	sorted := slices.Clone(values)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, v := range sorted {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, scrubValuePlaceholder)
	}
	return s
}
