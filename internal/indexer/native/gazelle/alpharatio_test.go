package gazelle

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	alphaRatioCookie   = "session=AR-SYNTHETIC-SESSION-0000000000"
	alphaRatioUsername = "ar-synthetic-user"
	alphaRatioPassword = "AR-SYNTHETIC-PASSWORD-0000000000"
)

func TestAlphaRatioDefinition(t *testing.T) {
	t.Parallel()

	family := familyByID(t, "alpharatio")
	def := family.Definition
	if def.RequestDelay == nil || *def.RequestDelay != alphaRatioDelaySeconds {
		t.Fatalf("RequestDelay = %v, want %v", def.RequestDelay, alphaRatioDelaySeconds)
	}
	type settingContract struct {
		secret   bool
		required bool
	}
	wantSettings := map[string]settingContract{
		"username":            {required: true},
		"password":            {secret: true, required: true},
		"use_freeleech_token": {},
		"freeleech_only":      {},
		"exclude_scene":       {},
	}
	if len(def.Settings) != len(wantSettings) {
		t.Errorf("settings count = %d, want %d", len(def.Settings), len(wantSettings))
	}
	seen := make(map[string]struct{}, len(def.Settings))
	for _, setting := range def.Settings {
		want, ok := wantSettings[setting.Name]
		if !ok {
			t.Errorf("unexpected setting %q", setting.Name)
			continue
		}
		if _, duplicate := seen[setting.Name]; duplicate {
			t.Errorf("duplicate setting %q", setting.Name)
		}
		seen[setting.Name] = struct{}{}
		if got := setting.IsSecret(); got != want.secret {
			t.Errorf("setting %q secret = %t, want %t", setting.Name, got, want.secret)
		}
		if setting.Required != want.required {
			t.Errorf("setting %q required = %t, want %t", setting.Name, setting.Required, want.required)
		}
	}
	for name := range wantSettings {
		if _, ok := seen[name]; !ok {
			t.Errorf("missing setting %q", name)
		}
	}

	driver := buildDriver(t, "alpharatio")
	if pager, ok := driver.(interface{ SupportsOffsetPaging() bool }); !ok || !pager.SupportsOffsetPaging() {
		t.Fatal("AlphaRatio must expose fixed-page upstream paging")
	}
	caps := driver.Capabilities()
	if got := caps.CategoryMap.MapTrackerCatToNewznab("9"); !slices.Contains(got, 2040) {
		t.Errorf("MovieHD category = %v, want Movies/HD (2040)", got)
	}
	if !slices.Contains(caps.Modes["movie-search"], "imdbid") {
		t.Errorf("movie-search modes = %v, want imdbid", caps.Modes["movie-search"])
	}
}

func TestAlphaRatioSearchAndParse(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("testdata/alpharatio_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{resp: mkResp(stdhttp.StatusOK, string(body))}
	d := searchDriver(t, "alpharatio", doer)
	d.Cfg = map[string]string{
		"cookie":              alphaRatioCookie,
		"username":            alphaRatioUsername,
		"password":            alphaRatioPassword,
		"freeleech_only":      "true",
		"exclude_scene":       "true",
		"use_freeleech_token": "true",
	}
	d.session = sessionState{cookie: alphaRatioCookie, generation: 1}

	releases, err := d.Search(context.Background(), search.Query{
		Keywords: "Example.Movie.2024",
		// Bare digits, as the torznab imdbid param actually arrives — the driver must
		// render the tt-prefixed zero-padded tag form AlphaRatio stores.
		IMDBID:     "1234567",
		Categories: []string{"9"},
		Offset:     50,
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	req := doer.reqs[0]
	if req.cookie != alphaRatioCookie || req.userAgent != alphaRatioUserAgent {
		t.Errorf("headers cookie=%q user-agent=%q", req.cookie, req.userAgent)
	}
	if req.authorization != "" || strings.Contains(req.url, alphaRatioCookie) {
		t.Errorf("AlphaRatio request leaked/misrouted auth: %+v", req)
	}
	parsedURL, err := url.Parse(req.url)
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}
	query := parsedURL.Query()
	for key, want := range map[string]string{
		"action": "browse", "searchstr": "Example Movie 2024", "taglist": "tt1234567",
		"freetorrent": "1", "scene": "0", "page": "2", "filter_cat[9]": "1",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}

	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(releases))
	}
	release := releases[0]
	if release.Title != "Example.Movie.2024.1080p.BluRay.x264-GROUP" || release.Size != 1234567890 || release.Files != 4 {
		t.Errorf("release core fields = %+v", release)
	}
	if release.Link != "https://alpharatio.cc/torrents.php?action=download&id=5500" {
		t.Errorf("Link = %q", release.Link)
	}
	if release.Details != "https://alpharatio.cc/torrents.php?id=4400&torrentid=5500" {
		t.Errorf("Details = %q", release.Details)
	}
	if release.IMDBID != "tt1234567" {
		t.Errorf("IMDBID = %q, want tt1234567 from tags", release.IMDBID)
	}
	if !slices.Equal(release.Categories, []int{2040}) || release.Seeders != 11 || release.Leechers != 2 || release.Peers != 13 {
		t.Errorf("release category/swarm = %+v", release)
	}
	if release.PublishDate != "2024-01-01T00:00:00Z" || release.DownloadVolumeFactor != 0 || release.UploadVolumeFactor != 1 {
		t.Errorf("release date/volume = %+v", release)
	}
	if release.MinimumRatio != 1 || release.MinimumSeedTime != 259200 {
		t.Errorf("minimums ratio=%v seedtime=%d", release.MinimumRatio, release.MinimumSeedTime)
	}
}

func TestAlphaRatioGrabUsesCookie(t *testing.T) {
	t.Parallel()

	doer := &seqDoer{resps: []*stdhttp.Response{mkTorrentResp(torrentBytes)}}
	d := grabDriver(t, "alpharatio", map[string]string{
		"cookie":   alphaRatioCookie,
		"username": alphaRatioUsername,
		"password": alphaRatioPassword,
	}, doer)
	link := d.downloadLink(5500, false)
	if _, err := d.Grab(context.Background(), link); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if got := doer.reqs[0]; got.cookie != alphaRatioCookie || got.userAgent != alphaRatioUserAgent || got.authorization != "" {
		t.Errorf("grab auth headers = %+v", got)
	}
}

func TestAlphaRatioTokenAndCredentialScrubbing(t *testing.T) {
	t.Parallel()

	d := parseDriver(t, "alpharatio", map[string]string{
		"cookie":              alphaRatioCookie,
		"username":            alphaRatioUsername,
		"password":            alphaRatioPassword,
		"use_freeleech_token": "true",
	})
	category := "MovieHD"
	release := d.nonMusicRelease(&group{
		GroupID:     flexInt(44),
		TorrentID:   flexInt(55),
		GroupName:   "Paid Example",
		Category:    &category,
		CanUseToken: true,
	})
	if release.Link != "https://alpharatio.cc/torrents.php?action=download&id=55&usetoken=1" {
		t.Errorf("token download link = %q", release.Link)
	}

	body := []byte(`{"status":"failure","error":"rejected ` + alphaRatioCookie + ` for ` + alphaRatioUsername + ` using ` + alphaRatioPassword + `"}`)
	_, err := d.parseBrowse(body, "")
	if err == nil {
		t.Fatal("want failure response error")
	}
	for _, secret := range []string{alphaRatioCookie, alphaRatioUsername, alphaRatioPassword} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked credential %q: %v", secret, err)
		}
	}
}

// TestAlphaRatioLoginPageValidation proves only an actual same-site logout link plus a
// usable response cookie can establish a session; forms and plain-text markers cannot.
func TestAlphaRatioLoginPageValidation(t *testing.T) {
	t.Parallel()
	const freshCookie = "AR-VALIDATED-SYNTHETIC-SESSION-0000000000"
	tests := []struct {
		name       string
		body       string
		withCookie bool
		wantCookie string
		wantErr    bool
	}{
		{
			name:       "valid session page",
			body:       `<nav><a href="/logout.php">Logout</a></nav>`,
			withCookie: true,
			wantCookie: "session=" + freshCookie,
		},
		{
			name:       "login form with cookie",
			body:       `<form id="loginform" action="/login.php"></form>`,
			withCookie: true,
			wantErr:    true,
		},
		{
			name:       "error text mentions logout path",
			body:       `<div class="error">Failed while loading logout.php</div>`,
			withCookie: true,
			wantErr:    true,
		},
		{
			name:    "authenticated page without cookie",
			body:    `<nav><a href="/logout.php">Logout</a></nav>`,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			doer := doerFunc(func(*stdhttp.Request) (*stdhttp.Response, error) {
				resp := mkResp(stdhttp.StatusOK, test.body)
				if test.withCookie {
					resp.Header.Add("Set-Cookie", (&stdhttp.Cookie{
						Name:     "session",
						Value:    freshCookie,
						Path:     "/",
						Secure:   true,
						HttpOnly: true,
						SameSite: stdhttp.SameSiteLaxMode,
					}).String())
				}
				return resp, nil
			})
			d := searchDriver(t, "alpharatio", doer)
			cookie, err := d.requestAlphaRatioLogin(context.Background(), url.Values{})
			if test.wantErr {
				if !errors.Is(err, login.ErrLoginFailed) {
					t.Fatalf("requestAlphaRatioLogin error = %v, want login.ErrLoginFailed", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("requestAlphaRatioLogin: %v", err)
			}
			if cookie != test.wantCookie {
				t.Errorf("cookie = %q, want %q", cookie, test.wantCookie)
			}
		})
	}
}

func TestAlphaRatioAutomaticSession(t *testing.T) {
	fixture, err := os.ReadFile("testdata/alpharatio_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tests := []struct {
		name          string
		initialCookie string
		run           func(context.Context, *driver) error
	}{
		{
			name: "login creates session before search",
			run: func(ctx context.Context, d *driver) error {
				_, searchErr := d.Search(ctx, search.Query{})
				return searchErr
			},
		},
		{
			name:          "expired search session renews",
			initialCookie: alphaRatioCookie,
			run: func(ctx context.Context, d *driver) error {
				_, searchErr := d.Search(ctx, search.Query{})
				return searchErr
			},
		},
		{
			name:          "expired grab session renews",
			initialCookie: alphaRatioCookie,
			run: func(ctx context.Context, d *driver) error {
				_, grabErr := d.Grab(ctx, d.downloadLink(5500, false))
				return grabErr
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const freshCookie = "AR-FRESH-SYNTHETIC-SESSION-0000000000"
			var loginCalls atomic.Int32
			var followedLoginGET atomic.Int32
			mux := stdhttp.NewServeMux()
			mux.HandleFunc("/login.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				if r.Method != stdhttp.MethodPost {
					followedLoginGET.Add(1)
					_, _ = w.Write([]byte(`<form id="loginform"></form>`))
					return
				}
				loginCalls.Add(1)
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse login form: %v", err)
				}
				if r.Form.Get("username") != alphaRatioUsername || r.Form.Get("password") != alphaRatioPassword || r.Form.Get("keeplogged") != "1" {
					t.Errorf("unexpected login form fields")
				}
				if r.Header.Get("User-Agent") != alphaRatioUserAgent {
					t.Errorf("login User-Agent = %q, want %q", r.Header.Get("User-Agent"), alphaRatioUserAgent)
				}
				//nolint:gosec // G124: synthetic httptest cookie must remain non-Secure so the HTTP test server receives it.
				stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "session", Value: freshCookie, Path: "/", HttpOnly: true})
				_, _ = w.Write([]byte(`<a href="/logout.php">Logout</a>`))
			})
			authenticated := func(w stdhttp.ResponseWriter, r *stdhttp.Request) bool {
				cookie, err := r.Cookie("session")
				if err != nil || cookie.Value != freshCookie {
					stdhttp.Redirect(w, r, "/login.php", stdhttp.StatusFound)
					return false
				}
				if r.Header.Get("User-Agent") != alphaRatioUserAgent {
					t.Errorf("request User-Agent = %q, want %q", r.Header.Get("User-Agent"), alphaRatioUserAgent)
				}
				return true
			}
			mux.HandleFunc("/ajax.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				if authenticated(w, r) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(fixture)
				}
			})
			mux.HandleFunc("/torrents.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				if authenticated(w, r) {
					w.Header().Set("Content-Type", "application/x-bittorrent")
					_, _ = w.Write([]byte(torrentBytes))
				}
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)

			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatalf("cookiejar.New: %v", err)
			}
			client := server.Client()
			client.Jar = jar
			client.CheckRedirect = apphttp.RedirectPolicy
			type persistCall struct{ name, value string }
			var persistMu sync.Mutex
			var persisted []persistCall
			cfg := map[string]string{"username": alphaRatioUsername, "password": alphaRatioPassword}
			if test.initialCookie != "" {
				cfg[alphaRatioCookieSetting] = test.initialCookie
			}
			built, err := New(native.Params{
				Def:     familyByID(t, "alpharatio").Definition,
				Cfg:     cfg,
				Doer:    client,
				BaseURL: server.URL,
				PersistSetting: func(_ context.Context, name, value string) error {
					persistMu.Lock()
					defer persistMu.Unlock()
					persisted = append(persisted, persistCall{name, value})
					return nil
				},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			d := built.(*driver)
			for range 2 {
				if err := test.run(context.Background(), d); err != nil {
					t.Fatalf("operation: %v", err)
				}
			}
			if got := loginCalls.Load(); got != 1 {
				t.Errorf("login calls = %d, want 1", got)
			}
			if got := followedLoginGET.Load(); got != 0 {
				t.Errorf("redirect policy followed login redirect %d times, want 0", got)
			}
			persistMu.Lock()
			defer persistMu.Unlock()
			if len(persisted) != 1 || persisted[0] != (persistCall{alphaRatioCookieSetting, "session=" + freshCookie}) {
				t.Errorf("persist calls = %+v, want one replacement cookie", persisted)
			}
		})
	}
}

// TestAlphaRatioCanceledLoginWaiterReturns proves initial-login and renewal callers stop
// waiting when their context is canceled while another login holds the gate.
func TestAlphaRatioCanceledLoginWaiterReturns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		wait func(context.Context, *driver) error
	}{
		{
			name: "initial session",
			wait: func(ctx context.Context, d *driver) error { return d.ensureSession(ctx) },
		},
		{
			name: "session renewal",
			wait: func(ctx context.Context, d *driver) error { return d.renewSession(ctx, 0) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertCanceledLoginWaiterReturns(t, test.wait)
		})
	}
}

// assertCanceledLoginWaiterReturns holds an active login at the transport boundary and
// verifies the supplied competing operation returns context.Canceled before release.
func assertCanceledLoginWaiterReturns(t *testing.T, wait func(context.Context, *driver) error) {
	t.Helper()
	const freshCookie = "AR-BLOCKED-LOGIN-SYNTHETIC-SESSION-0000000000"
	loginStarted := make(chan struct{})
	releaseLogin := make(chan struct{})
	var startOnce sync.Once
	doer := doerFunc(func(*stdhttp.Request) (*stdhttp.Response, error) {
		startOnce.Do(func() { close(loginStarted) })
		<-releaseLogin
		resp := mkResp(stdhttp.StatusOK, `<a href="/logout.php">Logout</a>`)
		resp.Header.Add("Set-Cookie", (&stdhttp.Cookie{
			Name:     "session",
			Value:    freshCookie,
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			SameSite: stdhttp.SameSiteLaxMode,
		}).String())
		return resp, nil
	})
	built, err := New(native.Params{
		Def: familyByID(t, "alpharatio").Definition,
		Cfg: map[string]string{
			"username": alphaRatioUsername,
			"password": alphaRatioPassword,
		},
		Doer: doer,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := built.(*driver)
	firstDone := make(chan error, 1)
	go func() { firstDone <- d.ensureSession(context.Background()) }()
	select {
	case <-loginStarted:
	case <-time.After(time.Second):
		t.Fatal("first login did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- wait(ctx, d) }()
	select {
	case waiterErr := <-waiterDone:
		if !errors.Is(waiterErr, context.Canceled) {
			t.Errorf("canceled waiter error = %v, want context.Canceled", waiterErr)
		}
	case <-time.After(time.Second):
		close(releaseLogin)
		<-firstDone
		waiterErr := <-waiterDone
		t.Fatalf("canceled waiter remained blocked; eventual error = %v", waiterErr)
	}
	close(releaseLogin)
	if err := <-firstDone; err != nil {
		t.Fatalf("first login: %v", err)
	}
}

func TestAlphaRatioAutomaticLoginFailureGuidance(t *testing.T) {
	t.Parallel()

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/ajax.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.Redirect(w, r, "/login.php", stdhttp.StatusFound)
	})
	mux.HandleFunc("/login.php", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte(`<form id="loginform"></form>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = apphttp.RedirectPolicy
	built, err := New(native.Params{
		Def: familyByID(t, "alpharatio").Definition,
		Cfg: map[string]string{
			"username": alphaRatioUsername,
			"password": alphaRatioPassword,
			"cookie":   alphaRatioCookie,
		},
		Doer:    client,
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = built.Search(context.Background(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("Search error = %v, want login.ErrLoginFailed", err)
	}
	message := strings.ToLower(err.Error())
	for _, required := range []string{"automatic login", "username/password"} {
		if !strings.Contains(message, required) {
			t.Errorf("error %q missing %q", err, required)
		}
	}
	if strings.Contains(message, "manually renew") {
		t.Errorf("error retained obsolete manual-cookie guidance: %q", err)
	}
}

func TestAlphaRatioConcurrentExpiryCoalescesLogin(t *testing.T) {
	t.Parallel()

	const freshCookie = "AR-CONCURRENT-FRESH-SESSION-0000000000"
	var staleRequests atomic.Int32
	var loginCalls atomic.Int32
	staleReady := make(chan struct{})
	var readyOnce sync.Once
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/ajax.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if cookie, err := r.Cookie("session"); err == nil && cookie.Value == freshCookie {
			_, _ = w.Write([]byte(`{"status":"success","response":{"results":[]}}`))
			return
		}
		if staleRequests.Add(1) == 2 {
			readyOnce.Do(func() { close(staleReady) })
		}
		select {
		case <-staleReady:
		case <-time.After(2 * time.Second):
			t.Error("timed out waiting for both stale requests")
			return
		}
		stdhttp.Redirect(w, r, "/login.php", stdhttp.StatusFound)
	})
	mux.HandleFunc("/login.php", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		loginCalls.Add(1)
		//nolint:gosec // G124: synthetic httptest cookie must remain non-Secure so the HTTP test server receives it.
		stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "session", Value: freshCookie, Path: "/", HttpOnly: true})
		_, _ = w.Write([]byte(`<a href="/logout.php">Logout</a>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = apphttp.RedirectPolicy
	built, err := New(native.Params{
		Def: familyByID(t, "alpharatio").Definition,
		Cfg: map[string]string{
			"username": alphaRatioUsername,
			"password": alphaRatioPassword,
			"cookie":   alphaRatioCookie,
		},
		Doer:    client,
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := built.(*driver)

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, searchErr := d.Search(context.Background(), search.Query{})
			errs <- searchErr
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Search: %v", err)
		}
	}
	if got := loginCalls.Load(); got != 1 {
		t.Errorf("login calls = %d, want one coalesced renewal", got)
	}
}

// alpharatioLoginMux builds a login+ajax server where the login POST hands out a fresh
// cookie each call and ajax accepts only cookies for which accept(value) is true. It
// reports the login-call count. Used to exercise the renew-on-first-rejected paths.
func alpharatioLoginMux(t *testing.T, accept func(value string) bool, loginCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/login.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Method != stdhttp.MethodPost {
			_, _ = w.Write([]byte(`<form id="loginform"></form>`))
			return
		}
		n := loginCalls.Add(1)
		//nolint:gosec // G124: synthetic httptest cookie must remain non-Secure so the HTTP test server receives it.
		stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "session", Value: fmt.Sprintf("AR-COOKIE-%d", n), Path: "/", HttpOnly: true})
		_, _ = w.Write([]byte(`<a href="/logout.php">Logout</a>`))
	})
	mux.HandleFunc("/ajax.php", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if c, err := r.Cookie("session"); err == nil && accept(c.Value) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","response":{"results":[]}}`))
			return
		}
		stdhttp.Redirect(w, r, "/login.php", stdhttp.StatusFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func alpharatioSessionDriver(t *testing.T, server *httptest.Server) *driver {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = apphttp.RedirectPolicy
	built, err := New(native.Params{
		Def:            familyByID(t, "alpharatio").Definition,
		Cfg:            map[string]string{"username": alphaRatioUsername, "password": alphaRatioPassword},
		Doer:           client,
		BaseURL:        server.URL,
		PersistSetting: func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.(*driver)
}

// TestAlphaRatioSessionRenewalOnRejectedCookie covers the generation-threading fix
// (#131 review) across both terminal outcomes: an empty session logs in (generation
// 0→1), and a rejected first cookie must RENEW (1→2) rather than reuse the rejected
// one — the pre-attempt generation snapshot mistook the in-attempt login for a
// concurrent renewal and skipped the second login. Both cases perform exactly one
// renewal (loginCalls==2); they differ only in whether that renewed cookie is accepted.
func TestAlphaRatioSessionRenewalOnRejectedCookie(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		accept  func(string) bool // which cookie the tracker accepts
		wantErr bool              // true: renewal still rejected → sentinel error; false: renewal accepted → success
	}{
		// AR-COOKIE-1 (first login) rejected, AR-COOKIE-2 (renewal) accepted → success.
		{name: "renewal accepted", accept: func(v string) bool { return v == "AR-COOKIE-2" }, wantErr: false},
		// Every cookie rejected → initial login, one renewal, then sessionRejected; the
		// terminal error must wrap login.ErrLoginFailed so the registry classifies it as auth.
		{name: "renewal still rejected", accept: func(string) bool { return false }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var loginCalls atomic.Int32
			server := alpharatioLoginMux(t, tt.accept, &loginCalls)
			d := alpharatioSessionDriver(t, server)

			_, err := d.Search(context.Background(), search.Query{})
			switch {
			case tt.wantErr && !errors.Is(err, login.ErrLoginFailed):
				t.Fatalf("Search error = %v, want errors.Is(login.ErrLoginFailed)", err)
			case !tt.wantErr && err != nil:
				t.Fatalf("Search after first-cookie rejection = %v, want success on renewal", err)
			}
			if got := loginCalls.Load(); got != 2 {
				t.Errorf("login calls = %d, want 2 (initial login + one renewal)", got)
			}
		})
	}
}
