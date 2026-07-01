package http

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestSafeTransportDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantEmpty  bool
		wantHas    []string
		wantNoLeak []string
	}{
		{
			name: "url.Error with query passkey surfaces host only",
			err: &url.Error{
				Op:  "Get",
				URL: "https://tracker.example/dl?id=42&passkey=deadbeefsecret",
				Err: errors.New("connection refused"),
			},
			wantHas:    []string{"Get", "https://tracker.example", "connection refused"},
			wantNoLeak: []string{"deadbeefsecret", "passkey", "id=42", "/dl"},
		},
		{
			name: "url.Error with PATH secret drops the path (beyond-hd shape)",
			err: &url.Error{
				Op:  "Get",
				URL: "https://beyond-hd.me/torrent/download/auto.12345.RSSKEY00000000000000000000000000",
				Err: errors.New("dial tcp: connection refused"),
			},
			wantHas:    []string{"https://beyond-hd.me", "connection refused"},
			wantNoLeak: []string{"RSSKEY00000000000000000000000000", "auto.12345", "/torrent/download"},
		},
		{
			name:       "unparseable url yields placeholder, no verbatim leak",
			err:        &url.Error{Op: "Get", URL: "https://exa mple/x?passkey=secretval", Err: errors.New("boom")},
			wantHas:    []string{redactedValue},
			wantNoLeak: []string{"secretval"},
		},
		{
			name:       "non-url error yields empty (caller keeps the fixed message)",
			err:        errors.New("read tcp: /path/PATHKEY-SECRET failed"),
			wantEmpty:  true,
			wantNoLeak: []string{"PATHKEY-SECRET"},
		},
		{
			name:      "nil error yields empty",
			err:       nil,
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SafeTransportDetail(tt.err)
			if tt.wantEmpty && got != "" {
				t.Fatalf("SafeTransportDetail = %q, want empty", got)
			}
			for _, want := range tt.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("detail %q missing %q", got, want)
				}
			}
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(got, leak) {
					t.Errorf("detail %q leaked %q", got, leak)
				}
			}
		})
	}
}
