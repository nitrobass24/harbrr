package selector

import "testing"

// TestNewtonsoftJSONString pins harbrr's reproduction of Newtonsoft.Json's
// default DateParseHandling.DateTime: an ISO-8601 (with "T") string VALUE in a
// JSON feed is reformatted to the .NET InvariantCulture general format
// (MM/dd/yyyy HH:mm:ss), which is what definitions like UNIT3D's created_at field
// (append " +00:00" -> dateparse "MM/dd/yyyy HH:mm:ss zzz") rely on. Strings that
// Newtonsoft's ISO parser does NOT recognize (space-separated, date-only, plain
// text) pass through unchanged.
func TestNewtonsoftJSONString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"iso utc micros", "2021-10-18T00:34:50.000000Z", "10/18/2021 00:34:50"},
		{"iso utc", "2026-06-13T16:09:54Z", "06/13/2026 16:09:54"},
		{"iso offset (wall clock)", "2021-10-18T00:34:50+02:00", "10/18/2021 00:34:50"},
		{"iso no zone", "2021-10-18T00:34:50", "10/18/2021 00:34:50"},
		{"space-separated stays raw", "2021-10-18 00:34:50", "2021-10-18 00:34:50"},
		{"date only stays raw", "2021-10-18", "2021-10-18"},
		{"plain title unchanged", "Big Buck Bunny 1080p (2008)", "Big Buck Bunny 1080p (2008)"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := newtonsoftJSONString(tt.in); got != tt.want {
				t.Errorf("newtonsoftJSONString(%q) = %q, want %q", tt.in, got, tt.want)
			}
			// Reached through the canonical-string chokepoint too.
			if got := canonicalString(tt.in); got != tt.want {
				t.Errorf("canonicalString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
