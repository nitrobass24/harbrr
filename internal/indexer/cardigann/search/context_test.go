package search

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
)

// ctxProbeKey types the sentinel context value the propagation test places on the
// request ctx, so the Doer can prove it received the caller's ctx.
type ctxProbeKey struct{}

// ctxDoer honors cancellation the way the stdlib *http.Client does (aborting when
// the request ctx is already done) and records the sentinel value carried on the
// request context (not the context itself, to satisfy containedctx), so a test can
// assert the threaded context both reaches the HTTP boundary and cancels there.
type ctxDoer struct {
	gotVal string
}

func (d *ctxDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if v, ok := req.Context().Value(ctxProbeKey{}).(string); ok {
		d.gotVal = v
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("<html></html>")),
		Request:    req,
	}, nil
}

// TestDoRequest_Context pins the search HTTP injection site (request.go's
// NewRequestWithContext, previously a hard-coded context.Background()): the
// caller's context must thread onto the *http.Request, so a sentinel value is
// observable at the Doer and a pre-cancelled context aborts with context.Canceled
// rather than running to completion.
func TestDoRequest_Context(t *testing.T) {
	t.Parallel()

	br := builtRequest{method: stdhttp.MethodGet, url: "https://t.invalid/browse"}

	t.Run("propagates the caller context", func(t *testing.T) {
		t.Parallel()
		doer := &ctxDoer{}
		ctx := context.WithValue(context.Background(), ctxProbeKey{}, "sentinel")
		if _, err := doRequest(ctx, doer, br, nil); err != nil {
			t.Fatalf("doRequest: %v", err)
		}
		if doer.gotVal != "sentinel" {
			t.Fatalf("request ctx missing caller value; got %q", doer.gotVal)
		}
	})

	t.Run("cancelled context aborts", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := doRequest(ctx, &ctxDoer{}, br, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("doRequest err = %v, want context.Canceled", err)
		}
	})
}
