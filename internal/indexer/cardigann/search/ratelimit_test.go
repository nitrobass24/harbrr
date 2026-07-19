package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := func() time.Time { return time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC) }
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "5", 5 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"garbage", "soon", 0},
		{"http-date future", "Thu, 01 Jan 2026 12:00:30 GMT", 30 * time.Second},
		{"http-date past", "Thu, 01 Jan 2026 11:59:30 GMT", 0},
		{"over cap", "100000", maxRetryAfter},
		// In-int but the *time.Second multiply would overflow int64 ns → cap, not wrap.
		{"in-int multiply-overflow", "9999999999", maxRetryAfter},
		// All digits but too large for int (strconv.ErrRange): must clamp to the cap,
		// NOT fall through to 0 ("retry immediately").
		{"oversized numeric clamps to cap", "999999999999999999999999999", maxRetryAfter},
		// Oversized NEGATIVE numeric is meaningless for a delay → 0 (not the cap).
		{"oversized negative numeric is zero", "-999999999999999999999999999", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseRetryAfter(tt.value, now); got != tt.want {
				t.Errorf("ParseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestRateLimitedError_IsAndAs(t *testing.T) {
	t.Parallel()
	err := &RateLimitedError{StatusCode: 429, RetryAfter: 2 * time.Second}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("RateLimitedError must match ErrRateLimited")
	}
	wrapped := fmt.Errorf("GET https://x: %w", err)
	if !errors.Is(wrapped, ErrRateLimited) {
		t.Fatal("wrapped RateLimitedError must still match ErrRateLimited")
	}
	var rl *RateLimitedError
	if !errors.As(wrapped, &rl) || rl.StatusCode != 429 || rl.RetryAfter != 2*time.Second {
		t.Fatalf("errors.As did not recover RateLimitedError: %+v", rl)
	}
}

// TestQuotaExceededError_UnwrapsToBothSentinels proves QuotaExceededError matches
// BOTH ErrRateLimited (so existing health/breaker classification needs no change)
// AND ErrQuotaExceeded (so the registry's budget tracker can additionally react to
// it), including through a wrapping fmt.Errorf %w.
func TestQuotaExceededError_UnwrapsToBothSentinels(t *testing.T) {
	t.Parallel()
	err := &QuotaExceededError{Detail: "daily limit reached"}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("QuotaExceededError must match ErrRateLimited")
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("QuotaExceededError must match ErrQuotaExceeded")
	}
	wrapped := fmt.Errorf("newznab: %w", err)
	if !errors.Is(wrapped, ErrRateLimited) || !errors.Is(wrapped, ErrQuotaExceeded) {
		t.Fatal("wrapped QuotaExceededError must still match both sentinels")
	}
	var qee *QuotaExceededError
	if !errors.As(wrapped, &qee) || qee.Detail != "daily limit reached" {
		t.Fatalf("errors.As did not recover QuotaExceededError: %+v", qee)
	}
}

// statusDoer returns a canned status + headers, recording nothing — for the
// doRequest classification tests.
type statusDoer struct {
	status int
	header stdhttp.Header
	body   string
}

func (d statusDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	h := d.header
	if h == nil {
		h = stdhttp.Header{}
	}
	return &stdhttp.Response{
		StatusCode: d.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

// TestDoRequest_RateLimitedStatus proves a plain Doer returning 429/503 makes
// doRequest mint the typed, status-bearing rate-limit error (with Retry-After) and
// that the passkey in the URL is redacted out of the error.
func TestDoRequest_RateLimitedStatus(t *testing.T) {
	t.Parallel()
	for _, code := range []int{stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		t.Run(stdhttp.StatusText(code), func(t *testing.T) {
			t.Parallel()
			h := stdhttp.Header{}
			h.Set("Retry-After", "7")
			passkey := "PK" + "SECRETVALUE12345"
			br := builtRequest{method: stdhttp.MethodGet, url: "https://t.invalid/browse?passkey=" + passkey}

			_, err := doRequest(context.Background(), statusDoer{status: code, header: h}, br, nil)
			if !errors.Is(err, ErrRateLimited) {
				t.Fatalf("err = %v, want ErrRateLimited", err)
			}
			var rl *RateLimitedError
			if !errors.As(err, &rl) || rl.StatusCode != code || rl.RetryAfter != 7*time.Second {
				t.Fatalf("RateLimitedError = %+v, want status %d retry 7s", rl, code)
			}
			if strings.Contains(err.Error(), passkey) {
				t.Errorf("error leaked passkey: %q", err.Error())
			}
		})
	}
}

// TestDoRequest_OtherNon2xxNotRateLimited proves a non-429/503 failure stays a
// generic error (not classified as rate_limited).
func TestDoRequest_OtherNon2xxNotRateLimited(t *testing.T) {
	t.Parallel()
	br := builtRequest{method: stdhttp.MethodGet, url: "https://t.invalid/browse"}
	_, err := doRequest(context.Background(), statusDoer{status: stdhttp.StatusInternalServerError}, br, nil)
	if err == nil {
		t.Fatal("want an error for HTTP 500")
	}
	if errors.Is(err, ErrRateLimited) {
		t.Errorf("HTTP 500 must not classify as rate_limited: %v", err)
	}
}

// TestDoSearchRequest_Non2xxFailsFast pins harbrr's DELIBERATE divergence from
// Jackett on the search path: a non-redirect non-2xx response (403/404/500) fails
// fast — the body never reaches the executor — even when that body would parse
// into rows in Jackett's HTML/XML branch. 429/503 stay the typed RateLimitedError.
// See checkStatus and parity/testdata/README.md, "Non-2xx search-status handling".
// The 3xx (redirect) half of logged-out detection is covered separately by
// TestDoSearchRequest_RedirectSurfacedAsData; this test guards the non-redirect
// statuses so the divergence is gated, not silent.
func TestDoSearchRequest_Non2xxFailsFast(t *testing.T) {
	t.Parallel()
	// A body a def's rows selector could match: in Jackett this would yield a
	// release (HTML) or 0-releases; harbrr must not surface it on a non-2xx.
	const parseableBody = `<html><body><table><tr class="row"><td>a release</td></tr></table></body></html>`
	for _, code := range []int{stdhttp.StatusForbidden, stdhttp.StatusNotFound, stdhttp.StatusInternalServerError} {
		t.Run(stdhttp.StatusText(code), func(t *testing.T) {
			t.Parallel()
			br := builtRequest{method: stdhttp.MethodGet, url: "https://t.invalid/browse"}
			sr, err := doSearchRequest(t.Context(), statusDoer{status: code, body: parseableBody}, br, nil)
			if err == nil {
				t.Fatalf("doSearchRequest surfaced HTTP %d body as data (%+v), want fail-fast error", code, sr)
			}
			if errors.Is(err, ErrRateLimited) {
				t.Errorf("HTTP %d must not classify as rate_limited: %v", code, err)
			}
			if strings.Contains(err.Error(), parseableBody) {
				t.Errorf("error leaked the response body: %q", err.Error())
			}
		})
	}
}

// TestDoSearchRequest_RateLimitedStatus proves the search path keeps the typed,
// status-bearing rate-limit error for 429/503 (the registry backs off on it) —
// these are NOT swept into the generic non-2xx fail-fast.
func TestDoSearchRequest_RateLimitedStatus(t *testing.T) {
	t.Parallel()
	for _, code := range []int{stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		t.Run(stdhttp.StatusText(code), func(t *testing.T) {
			t.Parallel()
			br := builtRequest{method: stdhttp.MethodGet, url: "https://t.invalid/browse"}
			_, err := doSearchRequest(t.Context(), statusDoer{status: code}, br, nil)
			if !errors.Is(err, ErrRateLimited) {
				t.Fatalf("err = %v, want ErrRateLimited", err)
			}
		})
	}
}
