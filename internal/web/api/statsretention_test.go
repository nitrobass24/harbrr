package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// retentionBody mirrors statsRetentionBody for assertions.
type retentionBody struct {
	Months int `json:"months"`
}

// TestStatsRetentionRoundTrip covers the operator dial: unset reports the default, a
// PUT persists and is echoed back by GET, and an out-of-range window is a 400 that
// leaves the stored value alone.
func TestStatsRetentionRoundTrip(t *testing.T) {
	t.Parallel()
	base, c := serve(t, authDisabledEnv(t))

	get := func() int {
		t.Helper()
		resp, body := do(t, c, http.MethodGet, base+"/api/config/stats-retention", nil, nil)
		mustStatus(t, resp, body, http.StatusOK)
		var got retentionBody
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		return got.Months
	}

	if months := get(); months != 12 {
		t.Errorf("unset retention = %d, want the 12-month default", months)
	}

	resp, body := do(t, c, http.MethodPut, base+"/api/config/stats-retention", retentionBody{Months: 6}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if months := get(); months != 6 {
		t.Errorf("retention after PUT = %d, want 6", months)
	}

	for _, bad := range []int{0, -1, 121} {
		resp, body := do(t, c, http.MethodPut, base+"/api/config/stats-retention", retentionBody{Months: bad}, nil)
		mustStatus(t, resp, body, http.StatusBadRequest)
	}
	if months := get(); months != 6 {
		t.Errorf("retention after rejected PUTs = %d, want the stored 6", months)
	}
}
