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

// TestScrubURLError proves a *url.Error's URL — which can carry an apikey/passkey in
// the query or userinfo credentials from a user-supplied base URL — never survives
// into the scrubbed error, while the operation and underlying cause remain; a non-URL
// error passes through unchanged. Consolidated from the announce and appsync copies.
func TestScrubURLError(t *testing.T) {
	t.Parallel()

	// A sentinel leaf cause, so the nested case can assert the ORIGINAL error survives in
	// the returned chain and not merely in the message text. Callers errors.Is/As through
	// a scrubbed error (the registry classifies a transport failure by its net.Error
	// cause), so swapping the %w wrap for %v would break them while leaving every
	// message assertion below green.
	errNestedCause := errors.New("boom")

	tests := []struct {
		name       string
		err        error
		wantHas    []string
		wantNoLeak []string
		// wantIs, when set, must remain reachable via errors.Is on the scrubbed result.
		wantIs error
	}{
		{
			name: "query apikey and passkey are dropped with the whole URL",
			err: &url.Error{
				Op:  "Post",
				URL: "http://harbrr:8787/api/indexers/tt/dl?apikey=feedsecret&passkey=NOTREALSECRET",
				Err: errors.New("dial tcp: connection refused"),
			},
			wantHas:    []string{"Post", "connection refused"},
			wantNoLeak: []string{"feedsecret", "NOTREALSECRET", "harbrr:8787"},
		},
		{
			name: "userinfo credentials in an app base URL are dropped",
			err: &url.Error{
				Op:  "Get",
				URL: "http://admin:sup3rsecret@sonarr:8989/api/v3/indexer",
				Err: errors.New("dial tcp: connection refused"),
			},
			wantHas:    []string{"Get", "connection refused"},
			wantNoLeak: []string{"sup3rsecret", "admin", "sonarr:8989"},
		},
		{
			name: "nested url.Error is scrubbed at every layer",
			err: &url.Error{
				Op:  "Get",
				URL: "https://outer.test/dl?apikey=OUTER-SECRET",
				Err: &url.Error{
					Op:  "parse",
					URL: "https://inner.test/rss/INNER-SECRET/feed",
					Err: errNestedCause,
				},
			},
			wantHas:    []string{"Get", "parse", "boom"},
			wantNoLeak: []string{"OUTER-SECRET", "INNER-SECRET", "outer.test", "inner.test"},
			wantIs:     errNestedCause,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scrubbed := ScrubURLError(tt.err)
			if tt.wantIs != nil && !errors.Is(scrubbed, tt.wantIs) {
				t.Errorf("scrubbed error %v dropped the original cause from its chain", scrubbed)
			}
			got := scrubbed.Error()
			for _, want := range tt.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("scrubbed error %q missing %q", got, want)
				}
			}
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(got, leak) {
					t.Errorf("scrubbed error %q leaked %q", got, leak)
				}
			}
		})
	}

	t.Run("non-url error passes through unchanged", func(t *testing.T) {
		t.Parallel()
		plain := errors.New("boom")
		// Identity, not errors.Is: the contract is "returned unchanged", and a
		// fmt.Errorf("...: %w", plain) wrap would satisfy errors.Is while violating it.
		// errorlint's "use errors.Is" rule is the right default everywhere else, but
		// here the pointer comparison IS the assertion.
		if got := ScrubURLError(plain); got != plain { //nolint:errorlint // identity is the contract under test; errors.Is would pass on a wrap
			t.Errorf("ScrubURLError altered a non-URL error: %v", got)
		}
	})
}

func TestRedactURLError(t *testing.T) {
	t.Parallel()

	secret := "PK" + "1111"

	t.Run("url.Error is rebuilt host-only", func(t *testing.T) {
		t.Parallel()
		uerr := &url.Error{Op: "Get", URL: "https://t.example/dl/" + secret + "?tk=" + secret, Err: errors.New("dial failed")}
		got := RedactURLError(uerr)
		if strings.Contains(got.Error(), secret) {
			t.Fatalf("rebuilt error leaked the URL secret: %q", got.Error())
		}
		if !strings.Contains(got.Error(), "https://t.example") || !strings.Contains(got.Error(), "dial failed") {
			t.Errorf("rebuilt error lost host or cause: %q", got.Error())
		}
	})

	t.Run("parse failure raw input is dropped", func(t *testing.T) {
		t.Parallel()
		// url.Parse quotes the FULL raw input into its *url.Error message; a %w
		// wrap of the raw error would leak it one layer below any redacted args.
		_, err := url.Parse("https://t.example/dl/" + secret + "/\x7f")
		if err == nil {
			t.Fatal("url.Parse should fail on the control character")
		}
		got := RedactURLError(err)
		if strings.Contains(got.Error(), secret) {
			t.Fatalf("rebuilt parse error leaked the raw input: %q", got.Error())
		}
	})

	t.Run("nested url.Error chain is scrubbed at every layer", func(t *testing.T) {
		t.Parallel()
		inner := &url.Error{Op: "parse", URL: "https://inner.example/dl/" + secret, Err: errors.New("inner cause")}
		outer := &url.Error{Op: "Get", URL: "https://outer.example/browse?tk=" + secret, Err: inner}
		got := RedactURLError(outer)
		if strings.Contains(got.Error(), secret) {
			t.Fatalf("nested chain leaked a URL secret: %q", got.Error())
		}
		if !strings.Contains(got.Error(), "https://outer.example") || !strings.Contains(got.Error(), "inner cause") {
			t.Errorf("nested chain lost the outer host or the innermost cause: %q", got.Error())
		}
	})

	t.Run("non-url.Error passes through", func(t *testing.T) {
		t.Parallel()
		plain := errors.New("plain cause")
		if got := RedactURLError(plain); got != plain { //nolint:errorlint // identity passthrough is the contract.
			t.Fatalf("RedactURLError(plain) = %v, want the same error", got)
		}
	})
}
