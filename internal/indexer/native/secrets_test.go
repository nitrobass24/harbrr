package native

import (
	"slices"
	"testing"
)

// TestSessionSecrets pins the length boundary: a real session token survives, the
// preference values a cookie header carries alongside it do not, and order is kept
// (ScrubValues sorts its own, but the caller's ordering is what a reader compares
// against the header).
func TestSessionSecrets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nothing", nil, nil},
		{"session token survives", []string{"AR-SYNTHETIC-SESSION-0000000000"}, []string{"AR-SYNTHETIC-SESSION-0000000000"}},
		{"preference values dropped", []string{"1", "en"}, nil},
		{"empty dropped", []string{""}, nil},
		{"boundary: 11 out, 12 in", []string{"abcdefghijk", "abcdefghijkl"}, []string{"abcdefghijkl"}},
		{
			"a whole cookie header and its long value, in order",
			[]string{"session=AR-SYNTHETIC-SESSION-0000000000; lang=en", "AR-SYNTHETIC-SESSION-0000000000", "en"},
			[]string{"session=AR-SYNTHETIC-SESSION-0000000000; lang=en", "AR-SYNTHETIC-SESSION-0000000000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SessionSecrets(tt.in...); !slices.Equal(got, tt.want) {
				t.Errorf("SessionSecrets(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
