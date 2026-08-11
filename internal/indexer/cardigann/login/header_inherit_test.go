package login

import (
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// The synthetic apikey below exists only to prove the header reached the wire.
const inheritKey = "synthetic-apikey-value"

// TestLoginHeadersSelector pins the null-coalescing rule itself: login.headers
// wins when declared (including an explicitly EMPTY map, which yaml unmarshals
// non-nil and which therefore suppresses the fallback exactly as C#'s `??`
// does), else search.headers.
func TestLoginHeadersSelector(t *testing.T) {
	t.Parallel()

	search := map[string][]string{"Authorization": {"Bearer from-search"}}
	loginHdr := map[string][]string{"X-Login": {"from-login"}}

	tests := []struct {
		name  string
		login map[string][]string
		want  map[string][]string
	}{
		{name: "nil login headers inherit search", login: nil, want: search},
		{name: "explicit empty suppresses fallback", login: map[string][]string{}, want: map[string][]string{}},
		{name: "declared login headers win", login: loginHdr, want: loginHdr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def := &loader.Definition{
				Login:  &loader.Login{Headers: tt.login},
				Search: loader.Search{Headers: search},
			}
			got := loginHeaders(def)
			if len(got) != len(tt.want) {
				t.Fatalf("loginHeaders() = %v, want %v", got, tt.want)
			}
			for k, want := range tt.want {
				if len(got[k]) != len(want) || (len(want) > 0 && got[k][0] != want[0]) {
					t.Errorf("loginHeaders()[%q] = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

// inheritDef is the HHD-shaped definition: a GET login probe with NO
// login.headers, whose auth header lives only in search.headers.
func inheritDef(test *loader.PageTestBlock) *loader.Definition {
	return &loader.Definition{
		Login: &loader.Login{
			Method: "get",
			Path:   "api/torrents",
			Test:   test,
		},
		Search: loader.Search{Headers: map[string][]string{
			"Authorization": {"Bearer {{ .Config.apikey }}"},
		}},
	}
}

// TestLoginGetInheritsSearchHeaders is the #454 regression: a get-method login
// with no login.headers must send search.headers — RENDERED — on the probe,
// else the tracker answers "API key not accepted".
func TestLoginGetInheritsSearchHeaders(t *testing.T) {
	t.Parallel()

	rt := newReplay(t, step{wantMethod: stdhttp.MethodGet, wantPath: "/api/torrents", bodyFile: "api_ok.html"})
	e := newExec(t, rt, map[string]string{"apikey": inheritKey})

	if err := e.Login(t.Context(), inheritDef(nil)); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := rt.capture(0).headers.Get("Authorization"); got != "Bearer "+inheritKey {
		t.Errorf("login GET Authorization = %q, want the rendered inherited header", got)
	}
}

// TestCheckTestInheritsSearchHeaders covers the login.test probe, which is the
// request an already-authenticated session actually repeats — including the
// single same-domain redirect follow, which re-sends the same inherited headers
// on the second hop.
func TestCheckTestInheritsSearchHeaders(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", status: stdhttp.StatusFound, respHeader: locationHeader("/home.php")},
		step{wantMethod: stdhttp.MethodGet, wantPath: "/home.php", bodyFile: "logged_in.html"},
	)
	e := newExec(t, rt, map[string]string{"apikey": inheritKey})

	def := inheritDef(&loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"})
	ok, err := e.CheckTest(t.Context(), def)
	if err != nil || !ok {
		t.Fatalf("CheckTest = (%v, %v), want (true, nil)", ok, err)
	}
	for i := range 2 {
		if got := rt.capture(i).headers.Get("Authorization"); got != "Bearer "+inheritKey {
			t.Errorf("login.test request %d Authorization = %q, want the rendered inherited header", i, got)
		}
	}
}

// TestLoginOneURLInheritsSearchHeaders covers the remaining GET-shaped method:
// the oneurl probe must carry the rendered inherited header too.
func TestLoginOneURLInheritsSearchHeaders(t *testing.T) {
	t.Parallel()

	rt := newReplay(t, step{wantMethod: stdhttp.MethodGet, wantPath: "/api/torrents", bodyFile: "api_ok.html"})
	e := newExec(t, rt, map[string]string{"apikey": inheritKey})

	def := inheritDef(nil)
	def.Login.Method = "oneurl"
	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := rt.capture(0).headers.Get("Authorization"); got != "Bearer "+inheritKey {
		t.Errorf("oneurl GET Authorization = %q, want the rendered inherited header", got)
	}
}

// TestLoginPostInheritsSearchHeaders closes the POST/form seam so a partial
// adoption of loginHeaders cannot regress the credential-submitting paths: the
// post method's submit, and the form method's landing GET *and* submit POST.
// Every request the flow issues must carry the inherited, rendered header.
func TestLoginPostInheritsSearchHeaders(t *testing.T) {
	t.Parallel()

	searchHeaders := loader.Search{Headers: map[string][]string{
		"Authorization": {"Bearer {{ .Config.apikey }}"},
	}}
	inputs := map[string]loader.Scalar{"username": scalar("{{ .Config.username }}")}

	tests := []struct {
		name  string
		steps []step
		login *loader.Login
	}{
		{
			name:  "post method",
			steps: []step{{wantMethod: stdhttp.MethodPost, wantPath: "/takelogin.php", bodyFile: "logged_in.html"}},
			login: &loader.Login{Method: "post", Path: "takelogin.php", Inputs: inputs},
		},
		{
			name: "form method landing + submit",
			steps: []step{
				{wantMethod: stdhttp.MethodGet, wantPath: "/login.php", bodyFile: "login_page.html"},
				{wantMethod: stdhttp.MethodPost, wantPath: "/takelogin.php", bodyFile: "logged_in.html"},
			},
			login: &loader.Login{Method: "form", Path: "login.php", Form: "form#loginform", Inputs: inputs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rt := newReplay(t, tt.steps...)
			e := newExec(t, rt, map[string]string{"username": "alice", "apikey": inheritKey})

			def := &loader.Definition{Login: tt.login, Search: searchHeaders}
			if err := e.Login(t.Context(), def); err != nil {
				t.Fatalf("Login: %v", err)
			}
			for i := range tt.steps {
				req := rt.capture(i)
				if got := req.headers.Get("Authorization"); got != "Bearer "+inheritKey {
					t.Errorf("request %d (%s): Authorization = %q, want the rendered inherited header", i, req.method, got)
				}
			}
			// The inherited map must not have displaced the form Content-Type.
			post := rt.capture(len(tt.steps) - 1)
			if ct := post.headers.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Errorf("login POST Content-Type = %q", ct)
			}
		})
	}
}
