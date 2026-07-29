package newznab

import (
	"context"
	"errors"
	"maps"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestParseUser proves the ?t=user document parses into the two daily caps, and that
// every shape a server might answer with instead — missing attributes, zero/negative/
// garbage values, an HTML page, a Newznab error envelope, a caps document — yields
// "nothing discovered" rather than a bogus cap.
func TestParseUser(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		wantQuery int
		wantGrab  int
	}{
		{name: "live probe shape", body: string(readGolden(t, "user.xml")), wantQuery: 2000, wantGrab: 1000},
		{name: "missing attributes", body: `<user username="x" role="VIP"/>`},
		{name: "blank attributes", body: `<user apirequests="" downloadrequests=""/>`},
		{name: "zero", body: `<user apirequests="0" downloadrequests="0"/>`},
		{name: "negative", body: `<user apirequests="-5" downloadrequests="-1"/>`},
		{name: "garbage", body: `<user apirequests="unlimited" downloadrequests="n/a"/>`},
		{name: "grab only", body: `<user downloadrequests="50"/>`, wantGrab: 50},
		{name: "html page", body: `<html><body>404 not found</body></html>`, wantErr: true},
		{name: "error envelope", body: `<error code="100" description="Incorrect user credentials"/>`, wantErr: true},
		{name: "caps document", body: string(readGolden(t, "caps.xml")), wantErr: true},
		{name: "not xml", body: `nope`, wantErr: true},
		{name: "empty", body: ``, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			user, err := parseUser([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseUser(%q) = %+v, want an error", tt.body, user)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUser: %v", err)
			}
			if got := user.queryCap(); got != tt.wantQuery {
				t.Errorf("queryCap = %d, want %d", got, tt.wantQuery)
			}
			if got := user.grabCap(); got != tt.wantGrab {
				t.Errorf("grabCap = %d, want %d", got, tt.wantGrab)
			}
		})
	}
}

// TestShouldSeed pins the seeding rules independently of any transport: only an UNSET
// cap is seeded, and a cap that already carries a value is never re-seeded — not even
// when discovery is stricter, because the provenance marker cannot tell an
// operator-edited value from a previously detected one.
func TestShouldSeed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		current    int
		discovered int
		want       bool
	}{
		{name: "unset is seeded", current: 0, discovered: 2000, want: true},
		{name: "nothing discovered", current: 0, discovered: 0},
		{name: "non-positive discovery never writes", current: 0, discovered: -1},
		{name: "operator cap never widened", current: 100, discovered: 2000},
		{name: "operator looser cap left alone", current: 5000, discovered: 2000},
		// A cap that already has a value is never rewritten, whoever set it: the marker
		// cannot tell an operator edit from an earlier probe, so re-seeding would risk
		// silently overwriting a typed value. Drift is covered by the learned latch.
		{name: "previously detected cap not re-seeded stricter", current: 2000, discovered: 500},
		{name: "previously detected cap not re-seeded looser", current: 500, discovered: 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldSeed(tt.current, tt.discovered); got != tt.want {
				t.Errorf("shouldSeed(%d, %d) = %v, want %v", tt.current, tt.discovered, got, tt.want)
			}
		})
	}
}

// TestSeedBudgetOnTest is the end-to-end seed: a Test against a server that answers
// ?t=user writes the two caps, their provenance markers, and the daily unit — and the
// probe costs exactly one request.
func TestSeedBudgetOnTest(t *testing.T) {
	t.Parallel()
	d, stored, hits := seedDriver(t, nil, userResponse{body: string(readGolden(t, "user.xml"))})
	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	want := map[string]string{
		settingQueryLimit:       "2000",
		settingGrabLimit:        "1000",
		settingQueryLimitSource: limitSourceDetected,
		settingGrabLimitSource:  limitSourceDetected,
		settingLimitsUnit:       "day",
	}
	assertStored(t, stored, want)
	if hits.Load() != 1 {
		t.Errorf("t=user hits = %d, want exactly 1", hits.Load())
	}
}

// TestSeedBudgetRespectsStoredSettings drives the seed through the full Test path for
// every stored-settings shape that matters: any cap that already has a value is left
// alone whoever set it and whatever discovery reports, only unset caps are filled, and
// limits_unit is never overwritten.
func TestSeedBudgetRespectsStoredSettings(t *testing.T) {
	t.Parallel()
	const body = `<user apirequests="2000" downloadrequests="1000"/>`
	tests := []struct {
		name      string
		cfg       map[string]string
		want      map[string]string
		wantProbe bool
	}{
		{
			name:      "operator caps are never touched",
			cfg:       map[string]string{settingQueryLimit: "100", settingGrabLimit: "10"},
			want:      map[string]string{},
			wantProbe: false,
		},
		{
			name:      "one operator cap, one unset",
			cfg:       map[string]string{settingQueryLimit: "100"},
			want:      map[string]string{settingGrabLimit: "1000", settingGrabLimitSource: limitSourceDetected, settingLimitsUnit: "day"},
			wantProbe: true,
		},
		{
			// A cap the operator may have EDITED since it was detected is not rewritten
			// — the marker records provenance, it cannot prove the value is still the
			// probe's. Overwriting here would silently replace a typed number; the
			// tracker-lowered-your-cap case is the reactive learned latch's job.
			name: "a previously detected cap is not re-seeded",
			cfg: map[string]string{
				settingQueryLimit: "5000", settingQueryLimitSource: limitSourceDetected,
				settingGrabLimit: "5000", settingGrabLimitSource: limitSourceDetected,
			},
			want:      map[string]string{},
			wantProbe: false,
		},
		{
			// Both caps already carry a value, so discovery has nothing it may say —
			// and the probe is skipped entirely rather than spent to learn that.
			name: "caps already set are left alone, and cost no request",
			cfg: map[string]string{
				settingQueryLimit: "50", settingQueryLimitSource: limitSourceDetected,
				settingGrabLimit: "5", settingGrabLimitSource: limitSourceDetected,
			},
			want:      map[string]string{},
			wantProbe: false,
		},
		{
			name:      "operator's hourly unit survives",
			cfg:       map[string]string{settingLimitsUnit: "hour"},
			want:      map[string]string{settingQueryLimit: "2000", settingQueryLimitSource: limitSourceDetected, settingGrabLimit: "1000", settingGrabLimitSource: limitSourceDetected},
			wantProbe: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, stored, hits := seedDriver(t, tt.cfg, userResponse{body: body})
			if err := d.Test(context.Background()); err != nil {
				t.Fatalf("Test: %v", err)
			}
			assertStored(t, stored, tt.want)
			if probed := hits.Load() > 0; probed != tt.wantProbe {
				t.Errorf("probed = %v, want %v (hits=%d)", probed, tt.wantProbe, hits.Load())
			}
		})
	}
}

// TestSeedBudgetDegradesSilently proves every failure mode of the optional t=user probe
// — a 404, a 500, an HTML page, an auth error envelope, an empty body — leaves the
// budget settings untouched AND leaves the test's own verdict green: an indexer without
// t=user is not a broken indexer.
func TestSeedBudgetDegradesSilently(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		resp userResponse
	}{
		{name: "404", resp: userResponse{status: stdhttp.StatusNotFound, body: "not found"}},
		{name: "500", resp: userResponse{status: stdhttp.StatusInternalServerError, body: "boom"}},
		{name: "rate limited", resp: userResponse{status: stdhttp.StatusTooManyRequests, body: "slow down"}},
		{name: "html login page", resp: userResponse{body: "<html><body>Sign in</body></html>"}},
		{name: "auth error envelope", resp: userResponse{body: `<error code="100" description="Incorrect user credentials"/>`}},
		{name: "empty body", resp: userResponse{body: ""}},
		{name: "caps echoed back", resp: userResponse{body: string(readGolden(t, "caps.xml"))}},
		{name: "zero caps", resp: userResponse{body: `<user apirequests="0" downloadrequests="0"/>`}},
		{name: "no cap attributes", resp: userResponse{body: `<user username="x" grabs="3"/>`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, stored, _ := seedDriver(t, nil, tt.resp)
			if err := d.Test(context.Background()); err != nil {
				t.Fatalf("Test = %v, want nil (a missing t=user must not fail the test)", err)
			}
			assertStored(t, stored, map[string]string{})
		})
	}
}

// TestSeedBudgetSkippedWithoutPersist proves the probe is not even issued when there is
// nowhere to persist the result (PersistSetting unwired) — no request is spent on a
// value that would be thrown away.
func TestSeedBudgetSkippedWithoutPersist(t *testing.T) {
	t.Parallel()
	var userHits atomic.Int64
	srv := userServer(t, &userHits, userResponse{body: string(readGolden(t, "user.xml"))})
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if userHits.Load() != 0 {
		t.Errorf("t=user hits = %d, want 0 (nothing could be persisted)", userHits.Load())
	}
}

// TestSeedLimitFailedCapWriteStaysRetryable proves a half-written seed does not strand
// the cap. The provenance marker is written before the cap, so when the cap write fails
// the cap stays UNSET — which is the only state shouldSeed will revisit, making the next
// Test seed it again. Writing the cap first would leave a cap with no marker that is
// never reconsidered.
func TestSeedLimitFailedCapWriteStaysRetryable(t *testing.T) {
	t.Parallel()
	var userHits atomic.Int64
	srv := userServer(t, &userHits, userResponse{body: `<user apirequests="2000" downloadrequests="1000"/>`})

	stored := map[string]string{}
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
		PersistSetting: func(_ context.Context, name, value string) error {
			if name == settingQueryLimit {
				return errors.New("store unavailable")
			}
			stored[name] = value
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if got, ok := stored[settingQueryLimit]; ok {
		t.Errorf("%s = %q, want unwritten so the next Test re-seeds it", settingQueryLimit, got)
	}
	if stored[settingQueryLimitSource] != limitSourceDetected {
		t.Errorf("%s = %q, want the marker written first (harmless without its cap)",
			settingQueryLimitSource, stored[settingQueryLimitSource])
	}
	// The grab cap is seeded independently and must be unaffected by the query failure.
	if stored[settingGrabLimit] != "1000" {
		t.Errorf("%s = %q, want 1000 (independent of the query cap's failure)", settingGrabLimit, stored[settingGrabLimit])
	}
}

// TestUserURLCarriesApikeyButRedacts mirrors the caps-URL check: the ?t=user URL must
// authenticate (so it carries the apikey) while every error path drops it.
func TestUserURLCarriesApikeyButRedacts(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	raw := d.buildAPIURL("user")
	if !strings.Contains(raw, "t=user") || !strings.Contains(raw, "apikey="+testAPIKey) {
		t.Fatalf("user URL = %q, want t=user and the apikey", redact(raw))
	}
	assertNoApikey(t, "redacted user URL", redact(raw))
}

// TestUserTransportErrorRedactsApikey proves the probe's transport error surfaces only
// the endpoint's scheme://host — the apikey-bearing URL never reaches the error string
// (which, though swallowed by seedBudget today, is the discipline every fetch owes).
func TestUserTransportErrorRedactsApikey(t *testing.T) {
	t.Parallel()
	const baseURL = "https://news.example.test"
	d, err := New(native.Params{
		Def: GenericDefinition(),
		Cfg: map[string]string{"apikey": testAPIKey},
		Doer: &errorDoer{err: &url.Error{
			Op:  "Get",
			URL: baseURL + "/api?t=user&apikey=" + testAPIKey,
			Err: errors.New("dial tcp: connection refused"),
		}},
		BaseURL: baseURL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, probeErr := d.(*driver).fetchUser(context.Background())
	if probeErr == nil {
		t.Fatal("fetchUser err = nil, want a transport error")
	}
	if !strings.Contains(probeErr.Error(), baseURL) {
		t.Errorf("error dropped the endpoint host: %q", probeErr.Error())
	}
	assertNoApikey(t, "user transport error", probeErr.Error())
}

// userResponse scripts what the offline server answers ?t=user with (status 0 means 200).
type userResponse struct {
	status int
	body   string
}

// userServer serves the caps golden for ?t=caps and the scripted response for ?t=user,
// counting the user probes.
func userServer(t *testing.T, userHits *atomic.Int64, resp userResponse) *httptest.Server {
	t.Helper()
	caps := readGolden(t, "caps.xml")
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") != "user" {
			_, _ = w.Write(caps)
			return
		}
		userHits.Add(1)
		if resp.status != 0 {
			w.WriteHeader(resp.status)
		}
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// seedDriver wires a driver with PersistSetting captured into the returned map, over a
// server that answers the caps golden and the scripted ?t=user response. cfg supplies the
// stored budget settings under test (the apikey/apiPath are added).
func seedDriver(t *testing.T, cfg map[string]string, resp userResponse) (*driver, map[string]string, *atomic.Int64) {
	t.Helper()
	var userHits atomic.Int64
	srv := userServer(t, &userHits, resp)

	settings := map[string]string{"apikey": testAPIKey, "apiPath": "/api"}
	maps.Copy(settings, cfg)
	stored := map[string]string{}
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     settings,
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
		PersistSetting: func(_ context.Context, name, value string) error {
			stored[name] = value
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver), stored, &userHits
}

// assertStored compares the budget settings actually written against want, ignoring the
// caps-cache entries every Test writes.
func assertStored(t *testing.T, stored, want map[string]string) {
	t.Helper()
	budget := map[string]string{}
	for _, key := range []string{settingQueryLimit, settingGrabLimit, settingLimitsUnit, settingQueryLimitSource, settingGrabLimitSource} {
		if v, ok := stored[key]; ok {
			budget[key] = v
		}
	}
	if !maps.Equal(budget, want) {
		t.Errorf("persisted budget settings = %v, want %v", budget, want)
	}
}
