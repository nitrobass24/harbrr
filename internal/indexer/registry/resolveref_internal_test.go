package registry

import (
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// resolveRef is the server side of the presence-aware PATCH: an absent update must
// keep the instance's current reference, a present-but-nil update clears it, and a
// present value sets it. This is what stops "rename the indexer" from wiping its
// proxy/solver wiring.
func TestResolveRef(t *testing.T) {
	t.Parallel()

	cur := int64(7)
	next := int64(9)
	tests := []struct {
		name    string
		update  domain.RefUpdate
		current *int64
		want    *int64
	}{
		{name: "absent keeps current", update: domain.RefUpdate{}, current: &cur, want: &cur},
		{name: "absent keeps nil current", update: domain.RefUpdate{}, current: nil, want: nil},
		{name: "present nil clears", update: domain.RefUpdate{Present: true, Value: nil}, current: &cur, want: nil},
		{name: "present value sets", update: domain.RefUpdate{Present: true, Value: &next}, current: &cur, want: &next},
		{name: "present value sets over nil current", update: domain.RefUpdate{Present: true, Value: &next}, current: nil, want: &next},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveRef(tt.update, tt.current)
			if !eqRef(got, tt.want) {
				t.Errorf("resolveRef(%+v, %v) = %v, want %v", tt.update, tt.current, got, tt.want)
			}
		})
	}
}

func eqRef(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
