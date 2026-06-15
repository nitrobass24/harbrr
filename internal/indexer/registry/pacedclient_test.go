package registry

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// --- test doubles ---

// scriptDoer serves a scripted sequence of (status, retry-after) responses (the
// last repeats) and records the request body seen on each call.
type scriptDoer struct {
	steps  []scriptStep
	calls  int
	bodies []string
}

type scriptStep struct {
	status     int
	retryAfter string
}

func (d *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		d.bodies = append(d.bodies, string(b))
	}
	i := d.calls
	d.calls++
	if i >= len(d.steps) {
		i = len(d.steps) - 1
	}
	s := d.steps[i]
	h := stdhttp.Header{}
	if s.retryAfter != "" {
		h.Set("Retry-After", s.retryAfter)
	}
	return &stdhttp.Response{
		StatusCode: s.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("body")),
		Request:    req,
	}, nil
}

// immediateTimer makes retry-go's backoff sleep return at once, recording the
// requested delays so a test can assert them (deterministic, no real waiting).
type immediateTimer struct{ delays []time.Duration }

func (t *immediateTimer) After(d time.Duration) <-chan time.Time {
	t.delays = append(t.delays, d)
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

// blockingTimer never fires, so a backoff sleep only ends via ctx cancellation.
type blockingTimer struct{}

func (blockingTimer) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

// unlimited is a limiter lookup that never paces, so retry/backoff tests are not
// slowed by the real per-host interval (pacing is tested separately).
func unlimited(string) *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }

func getReq(ctx context.Context, t *testing.T) *stdhttp.Request {
	t.Helper()
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, "https://t.invalid/browse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

// --- limiter ---

func TestLimiterFor(t *testing.T) {
	t.Parallel()
	a1 := limiterFor("host-a.test", time.Second)
	a2 := limiterFor("host-a.test", time.Second)
	b := limiterFor("host-b.test", time.Second)
	if a1 != a2 {
		t.Error("same host must reuse one limiter")
	}
	if a1 == b {
		t.Error("different hosts must get separate limiters")
	}
}

// TestRateSpacingViaReserve asserts the per-host pacing arithmetic deterministically
// (x/time/rate.Wait has no injectable clock, so spacing is checked via Reserve().Delay()).
func TestRateSpacingViaReserve(t *testing.T) {
	t.Parallel()
	const interval = 250 * time.Millisecond
	lim := rate.NewLimiter(rate.Every(interval), 1)
	// First token (the burst) is immediately available.
	if d := lim.Reserve().Delay(); d != 0 {
		t.Fatalf("first reservation delay = %v, want 0", d)
	}
	// The next token must wait ~one interval.
	d := lim.Reserve().Delay()
	if d <= 0 || d > interval {
		t.Fatalf("second reservation delay = %v, want (0, %v]", d, interval)
	}
}

// --- pacedDoer ---

func TestPacedDoer_SuccessNoRetry(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 200}}}
	d := newPacedDoer(base, time.Second)
	d.limiter = unlimited
	timer := &immediateTimer{}
	d.timer = timer

	resp, err := d.Do(getReq(context.Background(), t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 200 || base.calls != 1 || len(timer.delays) != 0 {
		t.Fatalf("calls=%d delays=%v, want 1 call no backoff", base.calls, timer.delays)
	}
}

func TestPacedDoer_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 429, retryAfter: "1"}, {status: 200}}}
	d := newPacedDoer(base, time.Second)
	d.limiter = unlimited
	timer := &immediateTimer{}
	d.timer = timer

	resp, err := d.Do(getReq(context.Background(), t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 200 || base.calls != 2 {
		t.Fatalf("calls=%d, want 2 (429 then 200)", base.calls)
	}
	if len(timer.delays) != 1 || timer.delays[0] != 1*time.Second {
		t.Fatalf("backoff delays=%v, want one 1s (honoring Retry-After)", timer.delays)
	}
}

func TestPacedDoer_ExhaustsToRateLimited(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 503}}} // always 503
	d := newPacedDoer(base, time.Second)
	d.limiter = unlimited
	d.timer = &immediateTimer{}

	_, err := d.Do(getReq(context.Background(), t))
	if !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) || rl.StatusCode != 503 {
		t.Fatalf("RateLimitedError = %+v, want status 503", rl)
	}
	if base.calls != maxRetryAttempts {
		t.Fatalf("base calls = %d, want %d (bounded, no loop)", base.calls, maxRetryAttempts)
	}
}

func TestPacedDoer_OtherStatusPassThrough(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 500}}}
	d := newPacedDoer(base, time.Second)
	d.limiter = unlimited
	d.timer = &immediateTimer{}

	resp, err := d.Do(getReq(context.Background(), t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 500 || base.calls != 1 {
		t.Fatalf("calls=%d status=%d, want one call, 500 passed through (no retry)", base.calls, resp.StatusCode)
	}
}

func TestPacedDoer_CancelDuringWait(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 200}}}
	d := newPacedDoer(base, time.Second)
	// A limiter whose only token is already spent: the next Wait blocks until the
	// (1h) interval — so a cancelled ctx must abort it before the request issues.
	drained := rate.NewLimiter(rate.Every(time.Hour), 1)
	drained.Allow()
	d.limiter = func(string) *rate.Limiter { return drained }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.Do(getReq(ctx, t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if base.calls != 0 {
		t.Fatalf("base called %d times, want 0 (aborted at Wait)", base.calls)
	}
}

func TestPacedDoer_CancelDuringBackoff(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel the moment the first 429 is returned, so the abort happens during the
	// (never-firing) backoff sleep, not at a Wait.
	base := &cancelOn429{cancel: cancel}
	d := newPacedDoer(base, time.Second)
	d.limiter = unlimited
	d.timer = blockingTimer{}

	_, err := d.Do(getReq(ctx, t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if base.calls != 1 {
		t.Fatalf("base called %d times, want 1 (aborted during backoff, no further attempt)", base.calls)
	}
}

type cancelOn429 struct {
	cancel context.CancelFunc
	calls  int
}

func (d *cancelOn429) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.calls++
	d.cancel()
	h := stdhttp.Header{}
	h.Set("Retry-After", "1")
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusTooManyRequests,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestPacedDoer_ResetsBodyOnRetry proves a POST body is re-sent on the retry (the
// stdlib sets GetBody for the strings.Reader bodies login/search build).
func TestPacedDoer_ResetsBodyOnRetry(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 429}, {status: 200}}}
	d := newPacedDoer(base, time.Second)
	d.limiter = unlimited
	d.timer = &immediateTimer{}

	req, err := stdhttp.NewRequestWithContext(context.Background(), stdhttp.MethodPost,
		"https://t.invalid/login", strings.NewReader("user=alice&pass=secret"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := d.Do(req); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(base.bodies) != 2 {
		t.Fatalf("body seen on %d calls, want 2", len(base.bodies))
	}
	for i, b := range base.bodies {
		if b != "user=alice&pass=secret" {
			t.Errorf("attempt %d body = %q, want the full form body re-sent", i, b)
		}
	}
}
