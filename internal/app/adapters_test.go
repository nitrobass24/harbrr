package app

import "testing"

// TestAnnounceOrigin covers the /dl base origin choice for an announce push: the
// configured server.external_url wins when set (issue #10's drift-cutting note),
// otherwise the connection's own stored harbrr URL, trailing slash trimmed.
func TestAnnounceOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		externalOrigin string
		harbrrURL      string
		want           string
	}{
		{"external_url set wins over the connection's URL", "https://harbrr.example.com", "http://10.0.0.5:7478/", "https://harbrr.example.com"},
		{"external_url unset falls back to the connection's URL", "", "http://10.0.0.5:7478/", "http://10.0.0.5:7478"},
		{"neither set", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := announceOrigin(tt.externalOrigin, tt.harbrrURL); got != tt.want {
				t.Errorf("announceOrigin(%q, %q) = %q, want %q", tt.externalOrigin, tt.harbrrURL, got, tt.want)
			}
		})
	}
}
