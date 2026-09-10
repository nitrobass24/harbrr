package dateparse

import (
	"strings"
	"testing"
)

// TestLocaleTablesNeverRewriteEnglishNames is the probe that found
// autobrr/harbrr#632: the French weekday abbr "mar" (mardi) registered as a
// rewrite key and turned the English month "Mar" into "Tue". Jackett parses
// with InvariantCulture first, so an English name in a value must always
// survive localization: no lookup-table key may equal an English month/day
// name and map to a different English name.
func TestLocaleTablesNeverRewriteEnglishNames(t *testing.T) {
	t.Parallel()
	for code, loc := range locales {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			for key, eng := range loc.lookupTable() {
				if _, english := englishNames[key]; english && !strings.EqualFold(key, eng) {
					t.Errorf("locale %q rewrites English name %q to %q", code, key, eng)
				}
			}
		})
	}
}
