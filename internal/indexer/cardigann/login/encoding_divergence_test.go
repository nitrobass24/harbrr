package login

import (
	"net/url"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/encode"
)

// TestLoginFormEncodingDivergence pins the DELIBERATE Phase 5 divergence between
// login form-body encoding and search-request encoding (see postForm in
// methods.go).
//
// Login bodies use stdlib url.Values.Encode: keys sorted alphabetically and
// values via url.QueryEscape, which percent-escapes ! * ( ) and leaves ~ literal.
// SEARCH requests use the .NET-compatible WebUtility encoder, which leaves
// ! * ( ) literal and percent-escapes ~. Both decode to the identical credential
// on the tracker side, so login is functionally unaffected; this divergence is
// accepted for Phase 5 (the parity replay harness asserts request method+URL
// only and discards POST bodies, and login inputs are typically alphanumeric).
//
// This test fails if login encoding is ever changed, forcing the change to be a
// conscious one that updates the disposition note.
func TestLoginFormEncodingDivergence(t *testing.T) {
	const password = "p@ss w!*()'~"

	loginBody := url.Values{"password": {password}}.Encode()

	// 1. The credential round-trips through the login encoding unchanged — the
	//    tracker receives the right value regardless of which encoder is used.
	parsed, err := url.ParseQuery(loginBody)
	if err != nil {
		t.Fatalf("login body did not parse: %v", err)
	}
	if got := parsed.Get("password"); got != password {
		t.Fatalf("login body did not round-trip password: got %q, want %q", got, password)
	}

	// 2. Pin the exact login-body wire encoding (url.Values.Encode): ! * ( )
	//    percent-escaped, space as '+', ~ left literal.
	const wantLogin = "password=p%40ss+w%21%2A%28%29%27~"
	if loginBody != wantLogin {
		t.Errorf("login form encoding = %q, want %q", loginBody, wantLogin)
	}

	// 3. The search encoder differs: ! * ( ) literal, ~ -> %7E. The two MUST NOT
	//    coincide — that is the documented divergence.
	searchEnc := encode.WebUtilityEncode(password)
	const wantSearch = "p%40ss+w!*()%27%7E"
	if searchEnc != wantSearch {
		t.Errorf("search WebUtility encoding = %q, want %q", searchEnc, wantSearch)
	}
	if "password="+searchEnc == loginBody {
		t.Error("login and search encodings unexpectedly coincide; the divergence note is stale")
	}
}
