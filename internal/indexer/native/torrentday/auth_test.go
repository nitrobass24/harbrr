package torrentday

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
)

// errDoer is a Doer that always returns a transport error whose message embeds the
// session cookie, proving the get() wrap scrubs it and preserves the error chain.
type errDoer struct{ err error }

func (e *errDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestGetTransportErrorScrubsCookie proves a transport error never leaks a secret. The real
// http.Client failure shape is a *url.Error whose Error() quotes its FULL URL — here a
// fabricated download URL hiding a secret in BOTH a path segment and a passkey query param,
// with an inner cause that also echoes the session cookie. get() must surface only
// scheme://host: SchemeHost(rawurl) + RedactURLError drop the path/query, and scrubSecrets
// strips the cookie.
func TestGetTransportErrorScrubsCookie(t *testing.T) {
	t.Parallel()
	const secret = "S3CRETTOKEN"
	cause := &url.Error{
		Op:  "Get",
		URL: "https://torrentday.example/dl/" + secret + "?passkey=" + secret,
		Err: errors.New("dial failed with Cookie=" + credCookie),
	}
	d := testDriver(t, nil, map[string]string{"cookie": credCookie})
	d.Doer = &errDoer{err: cause}
	_, err := d.get(context.Background(), base+"t.json?q=x", "application/json", false)
	if err == nil {
		t.Fatal("get: want an error, got nil")
	}
	msg := err.Error()
	// The session cookie is scrubbed...
	assertNoSecret(t, msg)
	// ...and so is the URL-embedded download secret: neither the raw token, nor its
	// /dl/<secret> path segment, nor the passkey=<secret> query survive.
	for _, leak := range []string{secret, "/dl/" + secret, "passkey=" + secret} {
		if strings.Contains(msg, leak) {
			t.Errorf("transport error leaks URL secret %q: %q", leak, msg)
		}
	}
	// The host is not a secret and MUST survive (RedactURLError keeps the *url.Error's
	// scheme://host while dropping its path/query).
	if !strings.Contains(msg, "https://torrentday.example") {
		t.Errorf("transport error dropped the host (scheme://host must survive): %q", msg)
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
