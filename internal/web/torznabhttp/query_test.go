package torznabhttp

import (
	"net/url"
	"strconv"
	"testing"
)

// TestParsePagingLimit pins the limit-clamping contract: a positive limit up to and
// including the advertised max is honored; anything above the max, zero, negative, or
// non-numeric falls back to the default (which equals the max). The `<=` bound lets a
// client request exactly the advertised max.
func TestParsePagingLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		limit string
		want  int
	}{
		{"absent", "", defaultLimit},
		{"one", "1", 1},
		{"mid", "25", 25},
		{"equals max", strconv.Itoa(defaultLimit), defaultLimit},
		{"above max clamps", strconv.Itoa(defaultLimit + 1), defaultLimit},
		{"far above max clamps", "100000", defaultLimit},
		{"zero", "0", defaultLimit},
		{"negative", "-5", defaultLimit},
		{"non-numeric", "abc", defaultLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tt.limit != "" {
				q.Set("limit", tt.limit)
			}
			if got := parsePaging(q).limit; got != tt.want {
				t.Errorf("parsePaging(limit=%q).limit = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

// TestParsePagingOffset pins the offset contract: a positive offset is honored;
// zero, negative, and non-numeric resolve to 0.
func TestParsePagingOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		offset string
		want   int
	}{
		{"absent", "", 0},
		{"positive", "50", 50},
		{"zero", "0", 0},
		{"negative", "-1", 0},
		{"non-numeric", "x", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tt.offset != "" {
				q.Set("offset", tt.offset)
			}
			if got := parsePaging(q).offset; got != tt.want {
				t.Errorf("parsePaging(offset=%q).offset = %d, want %d", tt.offset, got, tt.want)
			}
		})
	}
}
