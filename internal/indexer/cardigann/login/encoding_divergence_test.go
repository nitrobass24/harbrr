package login

import (
	"net/url"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/encode"
)

// TestLoginFormEncodingDivergence pins the (now single-character) divergence
// between login form-body encoding and search-request encoding (see postForm in
// methods.go).
//
// Login bodies use stdlib url.Values.Encode (url.QueryEscape): ! * ( ) percent-
// escaped, ~ left literal. SEARCH requests use WebUtilityEncode, which now also
// percent-escapes ! * ( ) (the on-the-wire form — a literal '(' trips some tracker
// WAFs; see the encode package) and additionally escapes ~ -> %7E. So the two now
// differ ONLY on '~'. Both decode to the identical credential on the tracker side.
//
// This test fails if either encoder is changed, forcing the change to be conscious.
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

	// 3. The search encoder now also escapes ! * ( ); it differs from login only by
	//    escaping ~ -> %7E. The two MUST NOT coincide — that one-char divergence is
	//    the documented disposition.
	searchEnc := encode.WebUtilityEncode(password)
	const wantSearch = "p%40ss+w%21%2A%28%29%27%7E"
	if searchEnc != wantSearch {
		t.Errorf("search WebUtility encoding = %q, want %q", searchEnc, wantSearch)
	}
	if "password="+searchEnc == loginBody {
		t.Error("login and search encodings unexpectedly coincide; the divergence note is stale")
	}
}
