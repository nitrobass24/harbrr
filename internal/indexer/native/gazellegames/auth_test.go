package gazellegames

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// quickUserBody is a successful request=quick_user response carrying the passkey.
const quickUserBody = `{"status":"success","response":{"passkey":"` + credPasskey + `"}}`

// TestFetchPasskeyPopulatesAndPersists proves the deferred passkey fetch runs: a
// request=quick_user GET reads the passkey into cfg, persists it, and only then is the
// served download URL non-empty (the bug was that no path ever populated the passkey, so
// every download URL carried an empty torrent_pass).
func TestFetchPasskeyPopulatesAndPersists(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: mkResp(stdhttp.StatusOK, quickUserBody)}
	d := apikeyOnlyDriver(t, doer)

	var persisted struct{ name, value string }
	d.persist = func(_ context.Context, name, value string) error {
		persisted.name, persisted.value = name, value
		return nil
	}

	// Before the fetch the download URL carries an empty torrent_pass.
	if got := d.downloadURL(42); !strings.Contains(got, "torrent_pass=&") && !strings.HasSuffix(got, "torrent_pass=") {
		t.Fatalf("pre-fetch download URL should have an empty passkey, got %q", got)
	}

	if err := d.fetchPasskey(context.Background()); err != nil {
		t.Fatalf("fetchPasskey: %v", err)
	}

	// quick_user was the request, and the apikey never rode the URL.
	if len(doer.reqs) != 1 || !strings.Contains(doer.reqs[0].url, "request=quick_user") {
		t.Fatalf("want a single request=quick_user GET, got %+v", doer.reqs)
	}
	if doer.reqs[0].apiKey != credAPIKey {
		t.Errorf("quick_user X-API-Key = %q, want the apikey", doer.reqs[0].apiKey)
	}
	if strings.Contains(doer.reqs[0].url, credAPIKey) {
		t.Errorf("quick_user URL leaks the apikey: %q", doer.reqs[0].url)
	}

	// The passkey is now in cfg, persisted, and present in the download URL.
	if d.cfgValue("passkey") != credPasskey {
		t.Errorf("cfg passkey = %q, want it populated", d.cfgValue("passkey"))
	}
	if persisted.name != "passkey" || persisted.value != credPasskey {
		t.Errorf("persisted = %+v, want passkey/%s", persisted, credPasskey)
	}
	if !strings.Contains(d.downloadURL(42), "torrent_pass="+credPasskey) {
		t.Errorf("post-fetch download URL missing the passkey: %q", d.downloadURL(42))
	}
}

// TestEnsurePasskeyReusesConfigured proves a configured passkey short-circuits the fetch
// (no quick_user round-trip), so a user/restored passkey is reused as-is.
func TestEnsurePasskeyReusesConfigured(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{}
	d := searchDriver(t, doer) // injects passkey: credPasskey
	if err := d.ensurePasskey(context.Background()); err != nil {
		t.Fatalf("ensurePasskey: %v", err)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("ensurePasskey made %d requests, want 0 (passkey already configured)", len(doer.reqs))
	}
}

// TestFetchPasskeyAuthFailures proves a non-success status, an empty passkey, and a
// 401/403 each surface as login.ErrLoginFailed (rather than silently serving an empty
// torrent_pass), and that no error leaks the apikey.
func TestFetchPasskeyAuthFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		resp *stdhttp.Response
	}{
		{"non-success status", mkResp(stdhttp.StatusOK, `{"status":"failure","error":"bad `+credAPIKey+`"}`)},
		{"empty passkey", mkResp(stdhttp.StatusOK, `{"status":"success","response":{"passkey":""}}`)},
		{"unauthorized", mkResp(stdhttp.StatusUnauthorized, "")},
		{"forbidden", mkResp(stdhttp.StatusForbidden, "")},
		{"malformed body", mkResp(stdhttp.StatusOK, "not json")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := apikeyOnlyDriver(t, &scriptDoer{resp: c.resp})
			err := d.fetchPasskey(context.Background())
			if !errors.Is(err, login.ErrLoginFailed) {
				t.Fatalf("err = %v, want login.ErrLoginFailed", err)
			}
			if strings.Contains(err.Error(), credAPIKey) {
				t.Errorf("error leaks the apikey: %v", err)
			}
		})
	}
}
