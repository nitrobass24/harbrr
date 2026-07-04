package api

import (
	"encoding/json"
	"testing"
)

// optionalRef is the tri-state that keeps a partial indexer PATCH from clearing a
// proxy/solver reference it never mentions. Its whole correctness rests on the fact
// that encoding/json calls UnmarshalJSON ONLY for a key that is present in the
// payload, so an absent key leaves present=false. Pin that here.
func TestOptionalRefDecode(t *testing.T) {
	t.Parallel()

	type payload struct {
		Name    *string     `json:"name"`
		ProxyID optionalRef `json:"proxyId"`
	}

	tests := []struct {
		name        string
		body        string
		wantPresent bool
		wantNil     bool // only meaningful when wantPresent
		wantValue   int64
	}{
		{name: "omitted key stays absent", body: `{"name":"x"}`, wantPresent: false},
		{name: "explicit null is present and clears", body: `{"proxyId":null}`, wantPresent: true, wantNil: true},
		{name: "number is present with value", body: `{"proxyId":5}`, wantPresent: true, wantValue: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p payload
			if err := json.Unmarshal([]byte(tt.body), &p); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tt.body, err)
			}
			got := p.ProxyID.toRegistry()
			if got.Present != tt.wantPresent {
				t.Fatalf("present = %v, want %v", got.Present, tt.wantPresent)
			}
			if !tt.wantPresent {
				return
			}
			switch {
			case tt.wantNil && got.Value != nil:
				t.Errorf("value = %d, want nil", *got.Value)
			case !tt.wantNil && (got.Value == nil || *got.Value != tt.wantValue):
				t.Errorf("value = %v, want %d", got.Value, tt.wantValue)
			}
		})
	}
}
