package http

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactError(t *testing.T) {
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
			// secretTokenRe alone stops at the first whitespace and would leak the
			// later pair; cookieHeaderRe must scrub the WHOLE multi-pair header value.
			"cookie header with multiple pairs scrubs all pairs",
			errors.New("request sent Cookie: session=AAA; cf_clearance=BBB; uid=CCC"),
			[]string{"AAA", "BBB", "CCC"},
			[]string{"<redacted>"},
		},
		{
			"apikey", errors.New("apikey=SECRETKEY was rejected"),
			[]string{"SECRETKEY"},
			[]string{"rejected"},
		},
		{
			// A JSON body (e.g. an *arr error echoing the pushed indexer) quotes the
			// key, so the value escapes secretTokenRe's key[=:] anchor; jsonSecretRe
			// scrubs the quoted form.
			"json-quoted apiKey", errors.New(`app rejected: {"apiKey":"HARBRRFEEDKEY","name":"x"}`),
			[]string{"HARBRRFEEDKEY"},
			[]string{"<redacted>", "name"},
		},
		{
			"json-quoted password with space", errors.New(`{"password": "p@ss w0rd-secret"}`),
			[]string{"p@ss w0rd-secret"},
			[]string{"<redacted>"},
		},
		{
			// An escaped quote inside the value must not end the match early and leak
			// the tail (the `tail-SECRET` after the \" would survive a naive `[^"]*`).
			"json-quoted secret with an escaped quote",
			errors.New(`{"apiKey":"head\"tail-SECRET","name":"x"}`),
			[]string{"head", "tail-SECRET"},
			[]string{"<redacted>", "name"},
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
			got := RedactError(tt.in)
			for _, s := range tt.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("redacted %q must NOT contain %q", got, s)
				}
			}
			for _, s := range tt.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("redacted %q must contain %q", got, s)
				}
			}
		})
	}
}

func TestRedactError_Nil(t *testing.T) {
	t.Parallel()
	if got := RedactError(nil); got != "" {
		t.Errorf("RedactError(nil) = %q, want empty", got)
	}
}
