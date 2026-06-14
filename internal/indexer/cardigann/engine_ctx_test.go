package cardigann

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
)

// ctxProbeKey types the sentinel context value the propagation tests place on the
// request ctx, so a Doer can prove it received the caller's ctx (not a fresh one).
type ctxProbeKey struct{}

// ctxHonoringDoer behaves like the stdlib *http.Client with respect to context:
// it aborts with the request context's error when that context is already done,
// and otherwise serves a saved body. It records the sentinel value carried on the
// request context (not the context itself, to satisfy containedctx), so a test can
// assert the caller's ctx reached the HTTP boundary. This is the offline stand-in
// that makes propagation and cancellation observable end-to-end without a network.
type ctxHonoringDoer struct {
	body   string
	gotVal string
}

func (d *ctxHonoringDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if v, ok := req.Context().Value(ctxProbeKey{}).(string); ok {
		d.gotVal = v
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

// TestEngineSearch_ContextCancelled proves a cancelled request context aborts the
// search at the HTTP boundary: the engine threads the caller's ctx through
// ensureSession -> search.Execute -> doRequest onto the *http.Request, so the Doer
// (like a real *http.Client) returns context.Canceled, which Search surfaces. A
// leftover context.Background() at request.go's NewRequestWithContext would make
// this run to completion instead.
func TestEngineSearch_ContextCancelled(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "html_scrape.yml")
	doer := &ctxHonoringDoer{body: string(readBody(t, "html_scrape.html"))}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call: the search must abort, not proceed.

	if _, err := eng.Search(ctx, Query{Keywords: "bunny"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search err = %v, want context.Canceled", err)
	}
}

// TestEngineSearch_ContextPropagates proves the exact caller ctx reaches the HTTP
// request: a sentinel value placed on the ctx is observable at the Doer, which can
// only happen if the engine threaded it through rather than minting its own
// background ctx.
func TestEngineSearch_ContextPropagates(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "html_scrape.yml")
	doer := &ctxHonoringDoer{body: string(readBody(t, "html_scrape.html"))}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	ctx := context.WithValue(context.Background(), ctxProbeKey{}, "sentinel")
	if _, err := eng.Search(ctx, Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if doer.gotVal != "sentinel" {
		t.Fatalf("request ctx did not carry the caller's value; got %q", doer.gotVal)
	}
}

// TestEngineTest_ContextCancelled proves the management Test action threads the
// request ctx into the login probe: a cancelled ctx aborts at the login request's
// HTTP boundary (Engine.Test -> EnsureLoggedIn -> Login -> ... -> do), surfaced as
// context.Canceled. This pins the decided asymmetry — Registry.Test already
// threads ctx; only engine.Test() dropped it.
func TestEngineTest_ContextCancelled(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "login_memo.yml") // login block (get), so the probe issues a request
	doer := &ctxHonoringDoer{body: string(readBody(t, "login_memo.html"))}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := eng.Test(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Test err = %v, want context.Canceled", err)
	}
}
