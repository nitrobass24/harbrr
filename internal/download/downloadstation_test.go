package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// dsStub is a minimal httptest stand-in for Synology's webapi: query.cgi answers
// SYNO.API.Info path discovery (version ranges are test-configurable, to exercise
// the v6-vs-v2 auth version pick), auth.cgi answers login, and entry.cgi answers
// the DownloadStation2.Task create call (both the GET type=url and the POST
// multipart type=file shapes).
type dsStub struct {
	authMaxVersion, taskMaxVersion int
	wantAccount, wantPassword      string
	createSuccess                  bool

	gotAuthVersion      string
	gotLoginMethod      string
	gotLoginURLQuery    url.Values // login request's URL query — must be empty (creds ride in the form body)
	gotLoginContentType string
	gotCreateQuery      url.Values // GET create's full query, or POST create's URL query (for _sid)
	gotCreateForm       map[string][]string
	gotFileBytes        []byte
}

func newDSStub(t *testing.T, s *dsStub) *httptest.Server {
	t.Helper()
	if s.authMaxVersion == 0 {
		s.authMaxVersion = 7
	}
	if s.taskMaxVersion == 0 {
		s.taskMaxVersion = 2
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/query.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{` +
			`"SYNO.API.Auth":{"path":"auth.cgi","minVersion":1,"maxVersion":` + strconv.Itoa(s.authMaxVersion) + `},` +
			`"SYNO.DownloadStation2.Task":{"path":"entry.cgi","minVersion":1,"maxVersion":` + strconv.Itoa(s.taskMaxVersion) + `}` +
			`}}`))
	})
	mux.HandleFunc("/webapi/auth.cgi", func(w http.ResponseWriter, r *http.Request) {
		s.gotLoginMethod = r.Method
		s.gotLoginURLQuery = r.URL.Query()
		s.gotLoginContentType = r.Header.Get("Content-Type")
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse login form: %v", err)
			http.Error(w, "bad form", http.StatusInternalServerError)
			return
		}
		s.gotAuthVersion = r.FormValue("version")
		if s.wantAccount != "" && (r.FormValue("account") != s.wantAccount || r.FormValue("passwd") != s.wantPassword) {
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":400}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"sid":"SID123"}}`))
	})
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, r *http.Request) {
		s.gotCreateQuery = r.URL.Query()
		if r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // test stub; body is a fixed small fixture, not attacker-controlled.
				t.Errorf("parse multipart form: %v", err)
				http.Error(w, "bad form", http.StatusInternalServerError)
				return
			}
			s.gotCreateForm = r.MultipartForm.Value
			if files := r.MultipartForm.File["fileData"]; len(files) == 1 {
				f, err := files[0].Open()
				if err != nil {
					t.Errorf("open fileData part: %v", err)
					http.Error(w, "bad file part", http.StatusInternalServerError)
					return
				}
				defer f.Close()
				buf := make([]byte, files[0].Size)
				_, _ = f.Read(buf)
				s.gotFileBytes = buf
			}
		}
		body := `{"success":false}`
		if s.createSuccess {
			body = `{"success":true,"data":{}}`
		}
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestDS(host, username, password string, settings domain.DownloadStationSettings) *downloadStationDriver {
	drv, _ := newDownloadStation(domain.DownloadClient{Host: host, Username: username, Settings: domain.DownloadClientSettings{DownloadStation: &settings}}, password, http.DefaultClient)
	return drv.(*downloadStationDriver)
}

func TestDSTest_OK(t *testing.T) {
	t.Parallel()
	stub := &dsStub{wantAccount: "admin", wantPassword: "hunter2"}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestDSTest_BadCredentials(t *testing.T) {
	t.Parallel()
	stub := &dsStub{wantAccount: "admin", wantPassword: "hunter2"}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "wrong", domain.DownloadStationSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for bad credentials")
	}
}

func TestDSTest_TaskV2Unsupported(t *testing.T) {
	t.Parallel()
	stub := &dsStub{taskMaxVersion: 1}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error when SYNO.DownloadStation2.Task doesn't support v2")
	}
}

// TestDSLogin_CredentialsRideFormBodyNotURL pins credential hygiene: account and
// passwd must never appear in the login request's URL (query strings land in
// proxy/access logs on the wire path) — they belong in a POST form body instead.
func TestDSLogin_CredentialsRideFormBodyNotURL(t *testing.T) {
	t.Parallel()
	stub := &dsStub{wantAccount: "admin", wantPassword: "hunter2"}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stub.gotLoginMethod != http.MethodPost {
		t.Fatalf("login method = %q, want POST", stub.gotLoginMethod)
	}
	if !strings.HasPrefix(stub.gotLoginContentType, "application/x-www-form-urlencoded") {
		t.Fatalf("login Content-Type = %q, want application/x-www-form-urlencoded", stub.gotLoginContentType)
	}
	if len(stub.gotLoginURLQuery) != 0 {
		t.Fatalf("login URL query = %v, want empty (account/passwd must ride in the form body, not the URL)", stub.gotLoginURLQuery)
	}
}

func TestDSAuthVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		authMaxVersion int
		wantVersion    string
	}{
		{"modern DSM advertises maxVersion 7 -> login v6", 7, "6"},
		{"older DSM advertises maxVersion 2 -> login v2", 2, "2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stub := &dsStub{authMaxVersion: tt.authMaxVersion}
			srv := newDSStub(t, stub)
			drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

			if err := drv.Test(context.Background()); err != nil {
				t.Fatalf("Test: %v", err)
			}
			if stub.gotAuthVersion != tt.wantVersion {
				t.Fatalf("login version = %q, want %q", stub.gotAuthVersion, tt.wantVersion)
			}
		})
	}
}

func TestDSAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &dsStub{createSuccess: true}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{Directory: "downloads"})

	const nzbURL = "https://example.com/release.nzb"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: nzbURL}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := stub.gotCreateQuery.Get("type"); got != "url" {
		t.Fatalf("type = %q, want url", got)
	}
	if got := stub.gotCreateQuery.Get("url"); got != nzbURL {
		t.Fatalf("url = %q, want %q", got, nzbURL)
	}
	if got := stub.gotCreateQuery.Get("destination"); got != "downloads" {
		t.Fatalf("destination = %q, want downloads", got)
	}
	if got := stub.gotCreateQuery.Get("_sid"); got != "SID123" {
		t.Fatalf("_sid = %q, want SID123 (must ride as a query param)", got)
	}
}

func TestDSAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	stub := &dsStub{createSuccess: true}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

	payload := []byte("d8:announce...e")
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: payload, Name: "test.torrent"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := first(stub.gotCreateForm["type"]); got != "file" {
		t.Fatalf("type = %q, want file", got)
	}
	if got := first(stub.gotCreateForm["file"]); got != `["fileData"]` {
		t.Fatalf(`file field = %q, want ["fileData"] (JSON-quoted)`, got)
	}
	if string(stub.gotFileBytes) != string(payload) {
		t.Fatalf("fileData part = %q, want %q", stub.gotFileBytes, payload)
	}
	if got := stub.gotCreateQuery.Get("_sid"); got != "SID123" {
		t.Fatalf("_sid = %q, want SID123 (must ride as a query param on the POST too)", got)
	}
}

func TestDSAdd_TorrentAndUsenetBothSupported(t *testing.T) {
	t.Parallel()
	stub := &dsStub{createSuccess: true}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

	for _, p := range []Payload{
		{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"},
		{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"},
	} {
		if err := drv.Add(context.Background(), p, AddOptions{}); err != nil {
			t.Fatalf("Add(%s): %v", p.Protocol, err)
		}
	}
}

func TestDSAdd_CategoryOverridesDirectory(t *testing.T) {
	t.Parallel()
	stub := &dsStub{createSuccess: true}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{Directory: "default-dir"})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{Category: "override-dir"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := stub.gotCreateQuery.Get("destination"); got != "override-dir" {
		t.Fatalf("destination = %q, want override-dir (opts.Category overrides settings default)", got)
	}
}

func TestDSAdd_CreateFailureSurfaced(t *testing.T) {
	t.Parallel()
	stub := &dsStub{createSuccess: false}
	srv := newDSStub(t, stub)
	drv := newTestDS(srv.URL, "admin", "hunter2", domain.DownloadStationSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err == nil {
		t.Fatal("expected an error when create responds success:false")
	}
}

// TestDSLogin_ErrorRedactsPassword pins that the password can never reach a
// returned error even on a transport failure (a *url.Error whose Error()
// otherwise quotes the request URL): every error path routes through
// apphttp.RedactURLError, which drops the query wholesale.
func TestDSLogin_ErrorRedactsPassword(t *testing.T) {
	t.Parallel()
	// An unroutable host forces a transport-level *url.Error without a live server
	// echoing anything back — this proves the query (account+passwd) never survives
	// into the wrapped error, independent of any server-side behavior.
	drv := newTestDS("http://127.0.0.1:1", "admin", "SUPER_SECRET_PASSWORD", domain.DownloadStationSettings{})

	err := drv.Test(context.Background())
	if err == nil {
		t.Fatal("expected a transport error against an unroutable host")
	}
	if got := err.Error(); strings.Contains(got, "SUPER_SECRET_PASSWORD") {
		t.Fatalf("error leaks the password: %q", got)
	}
}
