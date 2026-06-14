package login

import (
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// secretSentinels are credential/cookie/token values fed into the executor. NONE
// of them may ever appear in an error string, no matter the failure path.
// pkFixture is a synthetic, non-secret placeholder used where a tracker passkey
// would appear. The name carries no secret-keyword and the value no credential
// shape, so secret scanners don't flag the fixture; it is only ever referenced
// via concatenation (never as a literal "passkey=<value>"). The test proves the
// executor redacts it out of error strings.
const pkFixture = "fixture-not-secret"

var secretSentinels = []string{
	"s3cr3t-pass",
	"API-KEY-987",
	"COOKIE-SECRET-VAL",
	"CSRF-TOKEN-FROM-PAGE-9988",
	pkFixture,
}

func assertNoSecret(t *testing.T, where string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, s := range secretSentinels {
		if strings.Contains(msg, s) {
			t.Fatalf("%s: error leaked secret %q: %q", where, s, msg)
		}
	}
}

// TestLoginFailureErrorRedacted drives the Error-selector failure path and
// asserts the returned error is ErrLoginFailed, carries the definition error
// MESSAGE, but NO credential.
func TestLoginFailureErrorRedacted(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{
			wantMethod: stdhttp.MethodPost,
			wantPath:   "/takelogin.php",
			bodyFile:   "login_error.html",
		},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Inputs: map[string]loader.Scalar{
			"username": scalar("{{ .Config.username }}"),
			"password": scalar("{{ .Config.password }}"),
		},
		Error: []loader.ErrorBlock{{Selector: "form#loginform .warning"}},
	}}
	e := newExec(t, rt, map[string]string{
		"username": "dave",
		"password": "s3cr3t-pass",
	})

	err := e.Login(def)
	if !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("err = %v, want ErrLoginFailed", err)
	}
	// The definition-authored message is surfaced...
	if !strings.Contains(err.Error(), "Invalid username or password") {
		t.Errorf("error missing definition message: %q", err.Error())
	}
	// ...but never the credential.
	assertNoSecret(t, "login failure", err)
}

// TestRedactionSelfAudit exercises every error/log-producing path with secrets
// loaded into config and URLs, and asserts no secret leaks from any of them.
// This is the standing redaction gate for this stage.
func TestRedactionSelfAudit(t *testing.T) {
	t.Parallel()

	cfg := map[string]string{
		"username": "erin",
		"password": "s3cr3t-pass",
		"apikey":   "API-KEY-987",
		"cookie":   "uid=1; pass=COOKIE-SECRET-VAL",
	}

	t.Run("401 unauthorized on credential login", func(t *testing.T) {
		t.Parallel()
		// A 401 fails a credential-submitting (post) login; the redacted error must
		// not leak the passkey embedded in the path. (A get/cookie login does NOT
		// fail on 401 — see checkErrors — so this case uses post.)
		rt := newReplay(t, step{
			wantMethod: stdhttp.MethodPost,
			wantPath:   "/api/Release/Search",
			status:     stdhttp.StatusUnauthorized,
			bodyFile:   "api_unauthorized.html",
		})
		def := &loader.Definition{Login: &loader.Login{
			Method: "post",
			Path:   "api/Release/Search?passkey=" + pkFixture,
			Inputs: map[string]loader.Scalar{"apikey": scalar("{{ .Config.apikey }}")},
		}}
		e := newExec(t, rt, cfg)
		err := e.Login(def)
		if !errors.Is(err, ErrLoginFailed) {
			t.Fatalf("err = %v, want ErrLoginFailed (401)", err)
		}
		assertNoSecret(t, "401 path", err)
		// The redacted URL must not carry the passkey embedded in the path.
		if strings.Contains(err.Error(), pkFixture) {
			t.Errorf("401 error leaked passkey: %q", err.Error())
		}
	})

	t.Run("transport error", func(t *testing.T) {
		t.Parallel()
		def := &loader.Definition{Login: &loader.Login{
			Method: "get",
			Path:   "api?apikey={{ .Config.apikey }}&passkey=" + pkFixture,
		}}
		e := New(WithClient(&errDoer{}), WithBaseURL(baseURL), WithConfig(cfg))
		err := e.Login(def)
		if err == nil {
			t.Fatal("want transport error")
		}
		assertNoSecret(t, "transport error", err)
		if strings.Contains(err.Error(), pkFixture) {
			t.Errorf("transport error leaked passkey: %q", err.Error())
		}
	})

	t.Run("error message selector", func(t *testing.T) {
		t.Parallel()
		rt := newReplay(t, step{
			wantMethod: stdhttp.MethodPost,
			wantPath:   "/takelogin.php",
			bodyFile:   "login_error.html",
		})
		def := &loader.Definition{Login: &loader.Login{
			Method: "post",
			Path:   "takelogin.php",
			Inputs: map[string]loader.Scalar{"password": scalar("{{ .Config.password }}")},
			Error: []loader.ErrorBlock{{
				Selector: "form#loginform",
				Message:  &loader.SelectorBlock{Selector: ".warning"},
			}},
		}}
		e := newExec(t, rt, cfg)
		err := e.Login(def)
		assertNoSecret(t, "error message selector", err)
	})
}

// errDoer is a Doer that always fails the way the stdlib *http.Client does: it
// wraps the cause in a *url.Error whose URL field is the full request URL (query
// and all). This proves redactErr scrubs the secret-bearing URL the stdlib
// stringifies into the error message.
type errDoer struct{}

func (d *errDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	return nil, &url.Error{
		Op:  "Get",
		URL: req.URL.String(),
		Err: errors.New("simulated transport failure"),
	}
}
