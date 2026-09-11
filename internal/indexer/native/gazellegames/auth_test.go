package gazellegames

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"sync"
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

	// The passkey is now held on the driver, persisted, and present in the download URL.
	if d.passkey() != credPasskey {
		t.Errorf("passkey = %q, want it populated", d.passkey())
	}
	if persisted.name != "passkey" || persisted.value != credPasskey {
		t.Errorf("persisted = %+v, want passkey/%s", persisted, credPasskey)
	}
	if !strings.Contains(d.downloadURL(42), "torrent_pass="+credPasskey) {
		t.Errorf("post-fetch download URL missing the passkey: %q", d.downloadURL(42))
	}
}

// TestFetchPasskeyPersistFailureLeavesPasskeyEmpty proves the in-memory passkey is populated
// only AFTER persist succeeds: if persist fails, fetchPasskey returns the error AND leaves
// the learned passkey empty so ensurePasskey retries on the next search (live/stored must not
// diverge — publishing the passkey before a failed persist would make ensurePasskey stop retrying
// while the store has nothing).
func TestFetchPasskeyPersistFailureLeavesPasskeyEmpty(t *testing.T) {
	t.Parallel()
	// freshDoer serves a brand-new quick_user response per call so a retried fetch gets a
	// readable (not already-consumed) body.
	doer := &freshDoer{}
	d := apikeyOnlyDriver(t, doer)
	persistErr := errors.New("store unavailable")
	d.persist = func(_ context.Context, _, _ string) error { return persistErr }

	err := d.fetchPasskey(context.Background())
	if !errors.Is(err, persistErr) {
		t.Fatalf("fetchPasskey err = %v, want the persist error", err)
	}
	if got := d.passkey(); got != "" {
		t.Fatalf("passkey = %q, want empty after a failed persist (so ensurePasskey retries)", got)
	}
	// ensurePasskey must NOT short-circuit: a second attempt re-issues the quick_user fetch.
	if err := d.ensurePasskey(context.Background()); !errors.Is(err, persistErr) {
		t.Fatalf("ensurePasskey err = %v, want a retry hitting the persist error", err)
	}
	if doer.calls != 2 {
		t.Fatalf("want 2 quick_user requests (retry not short-circuited), got %d", doer.calls)
	}
}

// freshDoer returns a new quick_user success response on every call so a retried fetch is
// not served an already-drained body.
type freshDoer struct{ calls int }

func (f *freshDoer) Do(_ *stdhttp.Request) (*stdhttp.Response, error) {
	f.calls++
	return mkResp(stdhttp.StatusOK, quickUserBody), nil
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

// TestStorePasskeyScrubsStatusEcho proves the SERVER-CONTROLLED quick_user `status` field
// cannot leak a secret. The apikey rides in the X-API-Key header on that same request, so a
// server that echoes the apikey (or a configured passkey) back into its status is
// value-scrubbed via d.scrub before the status reaches the surfaced error (egress:
// error -> health Detail -> webhook). Fail-before: storePasskey rendered
// resp.Status.string() unscrubbed, so an echoed secret leaked.
func TestStorePasskeyScrubsStatusEcho(t *testing.T) {
	t.Parallel()
	d := searchDriver(t, &scriptDoer{}) // apikey + passkey both configured
	body := []byte(`{"status":"failure: key ` + credAPIKey + ` / pass ` + credPasskey + `","response":{"passkey":""}}`)
	err := d.storePasskey(context.Background(), body)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	if strings.Contains(err.Error(), credAPIKey) {
		t.Fatalf("storePasskey leaked the apikey via the status field: %q", err.Error())
	}
	if strings.Contains(err.Error(), credPasskey) {
		t.Fatalf("storePasskey leaked the passkey via the status field: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("expected [redacted] placeholder, got %q", err.Error())
	}
}

// TestStorePasskeyLeavesCfgAlone is the race guard: learning the passkey must not write
// into d.Cfg, the registry-owned settings map that other goroutines read without d.mu.
// Under -race, a concurrent reader of that map alongside the learning write would fail
// here (and a concurrent map write is a runtime fatal in production).
func TestStorePasskeyLeavesCfgAlone(t *testing.T) {
	t.Parallel()
	d := apikeyOnlyDriver(t, &scriptDoer{resp: mkResp(stdhttp.StatusOK, quickUserBody)})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				// A registry-side reader of the shared settings map, taking no driver lock.
				for k := range d.Cfg {
					_ = d.Cfg[k]
				}
			}
		}
	})

	for range 50 {
		if err := d.storePasskey(context.Background(), []byte(quickUserBody)); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("storePasskey: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	if d.passkey() != credPasskey {
		t.Errorf("passkey = %q, want it learned", d.passkey())
	}
	if _, ok := d.Cfg["passkey"]; ok {
		t.Error("storePasskey wrote the learned passkey into the shared settings map")
	}
}

// TestConcurrentPasskeyReadAndLearn drives the driver's own reader (the download URL
// build) against the learning write, which is the pairing that must be lock-clean.
func TestConcurrentPasskeyReadAndLearn(t *testing.T) {
	t.Parallel()
	d := apikeyOnlyDriver(t, &scriptDoer{resp: mkResp(stdhttp.StatusOK, quickUserBody)})

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			_ = d.downloadURL(42)
			_ = d.scrub("nothing to scrub")
		}
	})
	wg.Go(func() {
		for range 100 {
			if err := d.storePasskey(context.Background(), []byte(quickUserBody)); err != nil {
				t.Errorf("storePasskey: %v", err)
				return
			}
		}
	})
	wg.Wait()
}
