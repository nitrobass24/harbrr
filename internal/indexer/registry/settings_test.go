package registry

import "testing"

// TestSettingEnabled pins the fix for autobrr/harbrr#273: a checkbox-shaped setting
// persisted as the literal "false" (or any other value strconv.ParseBool recognizes as
// false) must read as OFF, not ON — the bare `!= ""` check it replaces treated any
// non-empty string, including "false", as checked.
func TestSettingEnabled(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty is off", "", false},
		{"false is off", "false", false},
		{"False is off", "False", false},
		{"FALSE is off", "FALSE", false},
		{"0 is off", "0", false},
		{"true is on", "true", true},
		{"True is on (cardigann's configTrue sentinel)", "True", true},
		{"1 is on", "1", true},
		{"yes is on (unparseable non-empty, permissive)", "yes", true},
		{"no is on (unparseable non-empty; ParseBool doesn't recognize \"no\" as false, so we deliberately stay permissive rather than guess)", "no", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := settingEnabled(tt.in); got != tt.want {
				t.Errorf("settingEnabled(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
