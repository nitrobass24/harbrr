package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// defaultFlareMaxTimeout is FlareSolverr's per-solve budget when unset; clamped to
// flareMaxTimeoutCap (a CF solve runs a real headless browser and is slow).
const (
	defaultFlareMaxTimeout = 60 * time.Second
	flareMaxTimeoutCap     = 180 * time.Second
)

// flareRequest / flareResponse are the typed FlareSolverr /v1 model (no
// map[string]any). Only the fields harbrr needs are modeled; unknown fields are
// ignored on decode.
type flareRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"` // milliseconds
}

type flareCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type flareSolution struct {
	URL       string        `json:"url"`
	UserAgent string        `json:"userAgent"`
	Cookies   []flareCookie `json:"cookies"`
}

type flareResponse struct {
	Status   string        `json:"status"`
	Message  string        `json:"message"`
	Solution flareSolution `json:"solution"`
}

// FlareSolverrSolver clears an anti-bot interstitial via a FlareSolverr instance
// (a real headless browser). It implements login.Solver: Solve posts a /v1
// request.get and returns the solution's cookies + User-Agent for harbrr to replay
// the real request itself (discard-and-replay, like Prowlarr). cf_clearance is
// UA-bound, so the caller MUST replay with the returned UA.
type FlareSolverrSolver struct {
	baseURL    string
	maxTimeout time.Duration
	client     *stdhttp.Client
}

// NewFlareSolverrSolver builds a solver against baseURL with the given per-solve
// maxTimeout. An out-of-range maxTimeout (<=0, or >180s) does NOT clamp to the
// bound — it resets to the 60s default (defaultFlareMaxTimeout). The HTTP client
// timeout is strictly greater than maxTimeout so the client never aborts a solve
// that is still within budget.
func NewFlareSolverrSolver(baseURL string, maxTimeout time.Duration) *FlareSolverrSolver {
	if maxTimeout <= 0 || maxTimeout > flareMaxTimeoutCap {
		maxTimeout = defaultFlareMaxTimeout
	}
	return &FlareSolverrSolver{
		baseURL:    strings.TrimRight(baseURL, "/"),
		maxTimeout: maxTimeout,
		client:     &stdhttp.Client{Timeout: maxTimeout + 30*time.Second},
	}
}

// Solve asks FlareSolverr to clear targetURL and returns the resulting cookies +
// User-Agent. A non-ok status, transport error, or bad response fails loud (the
// caller surfaces it as ErrSolverRequired -> an anti_bot health event). No secret
// (the base URL's embedded auth, cookies) is echoed into an error.
func (s *FlareSolverrSolver) Solve(ctx context.Context, targetURL string) (SolveResult, error) {
	return s.solve(ctx, flareRequest{Cmd: "request.get", URL: targetURL})
}

// solve sends one FlareSolverr /v1 request.get and returns the solution's cookies +
// User-Agent. fr.MaxTimeout is filled here so callers only specify the command and
// URL. (request.post is intentionally unused: FlareSolverr cannot complete a JS
// challenge mid-POST — see solveAndRetryLoginPost, which GET-solves then re-POSTs.)
func (s *FlareSolverrSolver) solve(ctx context.Context, fr flareRequest) (SolveResult, error) {
	if s.baseURL == "" {
		return SolveResult{}, fmt.Errorf("%w: flaresolverr_url is not configured", ErrNoSolverConfigured)
	}
	fr.MaxTimeout = int(s.maxTimeout / time.Millisecond)
	reqBody, err := json.Marshal(fr)
	if err != nil {
		return SolveResult{}, fmt.Errorf("flaresolverr: encode request: %w", err)
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, s.baseURL+"/v1", bytes.NewReader(reqBody))
	if err != nil {
		// The stdlib error embeds the full URL (flaresolverr_url may carry auth), so
		// don't interpolate it — the cause is always a malformed base URL.
		return SolveResult{}, errors.New("flaresolverr: invalid flaresolverr_url")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return SolveResult{}, fmt.Errorf("flaresolverr: %w", apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginBodyBytes))
	if err != nil {
		return SolveResult{}, fmt.Errorf("flaresolverr: read response: %w", err)
	}
	if resp.StatusCode != stdhttp.StatusOK {
		return SolveResult{}, fmt.Errorf("flaresolverr: returned HTTP %d", resp.StatusCode)
	}

	var resBody flareResponse
	if err := json.Unmarshal(body, &resBody); err != nil {
		return SolveResult{}, fmt.Errorf("flaresolverr: decode response: %w", err)
	}
	if resBody.Status != "ok" {
		// resBody.Message is FlareSolverr-authored; it can name the challenge but is
		// not echoed here to avoid surfacing any URL/identifier on the error path.
		return SolveResult{}, fmt.Errorf("flaresolverr: solve did not succeed (status %q)", resBody.Status)
	}

	cookies := make([]*stdhttp.Cookie, 0, len(resBody.Solution.Cookies))
	for _, c := range resBody.Solution.Cookies {
		cookies = append(cookies, &stdhttp.Cookie{Name: c.Name, Value: c.Value}) //nolint:gosec // request cookie; Set-Cookie security attrs are N/A
	}
	return SolveResult{Cookies: cookies, UserAgent: resBody.Solution.UserAgent}, nil
}
