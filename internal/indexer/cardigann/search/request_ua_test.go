package search

import (
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// TestApplySession_ReplaysSolverUA proves the search request carries the
// session's anti-bot solver User-Agent (a cf_clearance cookie is bound to it), and
// that a definition's own User-Agent header is left untouched.
func TestApplySession_ReplaysSolverUA(t *testing.T) {
	t.Parallel()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://t.invalid/browse")
	jar.SetCookies(u, []*stdhttp.Cookie{{Name: "cf_clearance", Value: "CFTOKEN"}}) //nolint:gosec // request cookie; Set-Cookie security attrs are N/A
	session := &login.Session{Jar: jar, UserAgent: "Mozilla/5.0 (solver)"}

	t.Run("sets UA and cookie when absent", func(t *testing.T) {
		t.Parallel()
		req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://t.invalid/browse", nil)
		applySession(req, session)
		if got := req.Header.Get("User-Agent"); got != "Mozilla/5.0 (solver)" {
			t.Errorf("User-Agent = %q, want the solver UA", got)
		}
		if c, err := req.Cookie("cf_clearance"); err != nil || c.Value != "CFTOKEN" {
			t.Errorf("cf_clearance cookie = %v (err %v), want CFTOKEN", c, err)
		}
	})

	t.Run("definition User-Agent wins", func(t *testing.T) {
		t.Parallel()
		req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://t.invalid/browse", nil)
		req.Header.Set("User-Agent", "DefUA")
		applySession(req, session)
		if got := req.Header.Get("User-Agent"); got != "DefUA" {
			t.Errorf("User-Agent = %q, want the definition's own UA to win", got)
		}
	})

	t.Run("no UA without a solve", func(t *testing.T) {
		t.Parallel()
		req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://t.invalid/browse", nil)
		applySession(req, &login.Session{Jar: jar})
		if got := req.Header.Get("User-Agent"); got != "" {
			t.Errorf("User-Agent = %q, want empty when the session carries no solver UA", got)
		}
	})
}
