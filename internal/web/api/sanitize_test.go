package api

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeTestError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		in             error
		mustNotContain []string
		mustContain    []string
	}{
		{
			"passkey key=value", errors.New("login failed: passkey=abc123secret"),
			[]string{"abc123secret"},
			[]string{"passkey", "<redacted>"},
		},
		{
			"cookie key: value", errors.New("seed cookie: cf_clearance=TOKENXYZ"),
			[]string{"TOKENXYZ"},
			[]string{"<redacted>"},
		},
		{
			"apikey", errors.New("apikey=SECRETKEY was rejected"),
			[]string{"SECRETKEY"},
			[]string{"rejected"},
		},
		{
			"transport error with passkey in URL",
			errors.New(`Get "https://t.test/rss?passkey=DEADBEEF": dial tcp: lookup failed`),
			[]string{"DEADBEEF"},
			[]string{"dial tcp"},
		},
		{
			"authorization bearer header",
			errors.New("upstream rejected request with Authorization: Bearer SECRETJWT.payload.sig"),
			[]string{"SECRETJWT", "payload.sig"},
			[]string{"<redacted>"},
		},
		{
			"authorization basic header",
			errors.New("Authorization=Basic dXNlcjpwYXNz failed"),
			[]string{"dXNlcjpwYXNz"},
			[]string{"failed"},
		},
		{
			"safe definition-authored message is unchanged",
			errors.New("invalid username or password"),
			nil,
			[]string{"invalid username or password"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeTestError(tt.in)
			for _, s := range tt.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("sanitized %q must NOT contain %q", got, s)
				}
			}
			for _, s := range tt.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("sanitized %q must contain %q", got, s)
				}
			}
		})
	}
}
