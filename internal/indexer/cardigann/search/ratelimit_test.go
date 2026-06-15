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

// statusDoer returns a canned status + headers, recording nothing — for the
// doRequest classification tests.
type statusDoer struct {
	status int
	header stdhttp.Header
}

func (d statusDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	h := d.header
	if h == nil {
		h = stdhttp.Header{}
	}
	return &stdhttp.Response{
		StatusCode: d.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("")),
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
