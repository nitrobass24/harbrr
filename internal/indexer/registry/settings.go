package registry

import "strconv"

// settingEnabled reports whether a persisted checkbox-shaped setting value ("freeleech",
// "freeleech_only", or any future on/off setting read via a presence check) means ON.
// Go-template truthiness treats any non-empty .Config value as set (a checked box
// resolves to "True", cardigann/config.go's configTrue), so a naive `!= ""` check reads
// a persisted literal "false" as CHECKED — the box the operator explicitly unchecked
// (autobrr/harbrr#273). This hardens that read in one place, independent of whether
// CanonicalizeCheckboxes ran first, whether the def types the setting as a checkbox at
// all, or what a future writer persists for "off":
//   - "" (absent/cleared) -> false
//   - anything strconv.ParseBool recognizes ("true"/"1"/"0"/"t"/"f", any case, including
//     cardigann's "True" sentinel) -> its parsed value
//   - any other non-empty string (e.g. a def's exotic truthy default) -> true, preserving
//     Go-template truthiness for values ParseBool doesn't understand
func settingEnabled(v string) bool {
	if v == "" {
		return false
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return true
}
