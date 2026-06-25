package torrentday

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"
)

// errDoer is a Doer that always returns a transport error whose message embeds the
// session cookie, proving the get() wrap scrubs it and preserves the error chain.
type errDoer struct{ err error }

func (e *errDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestGetTransportErrorScrubsCookie proves a transport error never leaks the cookie and
// that the underlying error chain is preserved (errors.Is still matches the cause).
func TestGetTransportErrorScrubsCookie(t *testing.T) {
	t.Parallel()
	cause := errors.New("dial failed with Cookie=" + credCookie)
	d := testDriver(t, nil, map[string]string{"cookie": credCookie})
	d.doer = &errDoer{err: cause}
	_, err := d.get(context.Background(), base+"t.json?q=x", "application/json")
	if err == nil {
		t.Fatal("get: want an error, got nil")
	}
	assertNoSecret(t, err.Error())
	if !errors.Is(err, cause) {
		t.Errorf("get error does not preserve the cause chain: %v", err)
	}
}

// TestScrubSecrets proves the configured cookie and User-Agent are removed from a string
// (so a wrapped transport error can never leak the session secret), and that an empty
// cfg is a no-op.
func TestScrubSecrets(t *testing.T) {
	t.Parallel()
	cfg := map[string]string{"cookie": credCookie, "user_agent": credUA}
	in := "dial failed for Cookie=" + credCookie + " UA=" + credUA
	out := scrubSecrets(in, cfg)
	if strings.Contains(out, credCookie) || strings.Contains(out, credUA) {
		t.Errorf("scrubSecrets left a secret: %q", out)
	}
	if !strings.Contains(out, "[REDACTED-COOKIE]") {
		t.Errorf("scrubSecrets did not insert the cookie placeholder: %q", out)
	}

	// An empty cfg leaves the string untouched.
	if got := scrubSecrets("plain message", map[string]string{}); got != "plain message" {
		t.Errorf("scrubSecrets(empty cfg) = %q, want unchanged", got)
	}
}
