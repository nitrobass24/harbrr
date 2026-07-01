package registry

import (
	"bytes"
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
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
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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

// TestPacedDoer_DebugLogsHostOnly asserts the debug trace records the request method,
// status, and only the scheme://host — never the path or query — so a secret hidden in
// EITHER a query param OR a PATH segment (as native drivers do) can never leak into the
// log. This is the fix for the F2 review finding that RedactURL's path heuristic misses
// a native path-embedded api_key/rsskey/passkey.
func TestPacedDoer_DebugLogsHostOnly(t *testing.T) {
	t.Parallel()
	const queryKey = "querysecretdeadbeefdeadbeef0000"
	const pathKey = "PATHSECRET-not-hex-so-heuristic-misses-it"
	var buf bytes.Buffer
	log := zerolog.New(&buf).Level(zerolog.DebugLevel)

	base := &scriptDoer{steps: []scriptStep{{status: 200}}}
	d := newPacedDoer(base, time.Second, log)
	d.limiter = unlimited

	req, err := stdhttp.NewRequestWithContext(context.Background(), stdhttp.MethodGet,
		"https://t.invalid/torrent/download/auto.5."+pathKey+"?passkey="+queryKey, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := d.Do(req); err != nil {
		t.Fatalf("Do: %v", err)
	}

	out := buf.String()
	for _, leak := range []string{queryKey, pathKey, "download", "passkey", "auto.5"} {
		if strings.Contains(out, leak) {
			t.Errorf("debug log leaked %q: %s", leak, out)
		}
	}
	for _, want := range []string{`"method":"GET"`, `"status":200`, `"url":"https://t.invalid"`, "outbound request"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug log missing %q in: %s", want, out)
		}
	}
}

func TestPacedDoer_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 429, retryAfter: "1"}, {status: 200}}}
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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

// TestPacedDoer_BudgetBoundsCumulativeWait proves the cumulative waits + backoff
// sleeps are bounded even when the inbound context carries NO deadline. A hostile
// tracker returns 503 with a huge Retry-After and the backoff timer never fires on
// its own, so only the budget can stop Do. It must end quickly (not pin the goroutine
// for the attacker's hour) and surface the typed RATE-LIMITED error — the budget
// capping a hostile backoff is not a caller abort.
func TestPacedDoer_BudgetBoundsCumulativeWait(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 503, retryAfter: "3600"}}}
	d := newPacedDoer(base, time.Second, zerolog.Nop())
	d.limiter = unlimited
	d.timer = blockingTimer{} // backoff never fires; only the budget can end the sleep
	d.budget = 40 * time.Millisecond

	start := time.Now()
	_, err := d.Do(getReq(context.Background(), t))
	elapsed := time.Since(start)

	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want a RateLimitedError (budget capped the hostile backoff, not a caller abort)", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, must NOT be a 'request aborted' deadline — the caller never cancelled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Do took %v, want ~budget (cumulative time not bounded)", elapsed)
	}
	if base.calls != 1 {
		t.Fatalf("base called %d times, want 1 (budget fired during the first backoff)", base.calls)
	}
}

// slowErrDoer sleeps then returns a fixed transport error, simulating a base.Do that
// outlasts the budget and then fails.
type slowErrDoer struct {
	sleep time.Duration
	err   error
	calls int
}

func (d *slowErrDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) {
	d.calls++
	time.Sleep(d.sleep)
	return nil, d.err
}

// TestPacedDoer_SurfacesSlowTransportError proves a substantive base.Do error is
// surfaced AS-IS even when the (uncancelled) call ran long enough to exhaust the
// budget — it must NOT be masked as a "request aborted" deadline, so the real cause
// stays diagnosable. Regression for the error-reclassification bug.
func TestPacedDoer_SurfacesSlowTransportError(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("connection reset by peer")
	base := &slowErrDoer{sleep: 40 * time.Millisecond, err: errBoom}
	d := newPacedDoer(base, time.Second, zerolog.Nop())
	d.limiter = unlimited
	d.budget = 10 * time.Millisecond // expires while base.Do is still running

	_, err := d.Do(getReq(context.Background(), t))
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want it to wrap the real transport error %v", err, errBoom)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, must NOT be masked as a budget/deadline abort", err)
	}
	if base.calls != 1 {
		t.Fatalf("base called %d times, want 1", base.calls)
	}
}

// slowThenOKDoer sleeps on its first call and returns 429, then returns 200. It
// proves the pacing budget is NOT consumed by a slow live response.
type slowThenOKDoer struct {
	sleep time.Duration
	calls int
}

func (d *slowThenOKDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.calls++
	status := 200
	if d.calls == 1 {
		time.Sleep(d.sleep)
		status = 429
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("body")),
		Request:    req,
	}, nil
}

// TestPacedDoer_SlowResponseDoesNotConsumeBudget proves a slow live 429 does not eat
// the pacing budget: base.Do runs on the request context, not the budget, so the
// budget stays available for the backoff and the retry still fires. Under the old
// single-waitCtx design this would abort after one call.
func TestPacedDoer_SlowResponseDoesNotConsumeBudget(t *testing.T) {
	t.Parallel()
	base := &slowThenOKDoer{sleep: 30 * time.Millisecond}
	d := newPacedDoer(base, time.Second, zerolog.Nop())
	d.limiter = unlimited
	d.timer = &immediateTimer{}
	d.budget = 15 * time.Millisecond // shorter than the slow response, but caps only waits/sleeps

	resp, err := d.Do(getReq(context.Background(), t))
	if err != nil {
		t.Fatalf("Do: %v (a slow live 429 must not consume the pacing budget)", err)
	}
	if resp.StatusCode != 200 || base.calls != 2 {
		t.Fatalf("status=%d calls=%d, want 200 after a retry (budget intact despite the slow first response)", resp.StatusCode, base.calls)
	}
}

// TestPacedDoer_InboundDeadlineWinsOverBudget proves a shorter inbound deadline still
// bounds the cumulative sleeps (the budget is only a ceiling): with a never-firing
// backoff, the request's own deadline ends Do.
func TestPacedDoer_InboundDeadlineWinsOverBudget(t *testing.T) {
	t.Parallel()
	base := &scriptDoer{steps: []scriptStep{{status: 503, retryAfter: "3600"}}}
	d := newPacedDoer(base, time.Second, zerolog.Nop())
	d.limiter = unlimited
	d.timer = blockingTimer{}
	d.budget = time.Hour // far larger than the inbound deadline below

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.Do(getReq(ctx, t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded (inbound deadline)", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Do took %v, want ~inbound deadline", elapsed)
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
	d := newPacedDoer(base, time.Second, zerolog.Nop())
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
