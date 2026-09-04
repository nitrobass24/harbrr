package search

import (
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/regexadapter"
)

// TestIsSkippableRowError pins which per-row failures the HTML parse loop is
// allowed to swallow.
//
// Jackett wraps each HTML row in a try/catch and drops the offending row, which
// is why a malformed row must stay skippable -- one bad row never throws away the
// page. A ReDoS-guard timeout is the exception: it means the match was abandoned
// on a clock, so the row's contents are unknown rather than known-bad. Swallowing
// it returns a short result set with nothing to say why, which is how a parity
// mismatch ends up indistinguishable from a selector that matched nothing.
func TestIsSkippableRowError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "ordinary row failure is skippable",
			err:  errors.New("required field \"title\" not found"),
			want: true,
		},
		{
			name: "wrapped ordinary failure is skippable",
			err:  fmt.Errorf("parsing row 3: %w", errors.New("bad date")),
			want: true,
		},
		{
			name: "ReDoS timeout is NOT skippable",
			err:  regexadapter.ErrMatchTimeout,
			want: false,
		},
		{
			// The pipeline wraps as it unwinds, so the check must survive nesting --
			// regexadapter itself returns "regexp2 match: %w" around the sentinel.
			name: "wrapped ReDoS timeout is NOT skippable",
			err:  fmt.Errorf("field title: %w", fmt.Errorf("regexp2 match: %w", regexadapter.ErrMatchTimeout)),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSkippableRowError(tt.err); got != tt.want {
				t.Errorf("isSkippableRowError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
