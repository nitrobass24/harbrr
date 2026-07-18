package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/go-rtorrent/xmlrpc"

	"github.com/autobrr/harbrr/internal/domain"
)

// rtorrentStub is a minimal httptest stand-in for rTorrent's XML-RPC endpoint.
// It decodes the incoming methodCall with the vendor's own xmlrpc.Unmarshal
// (rather than reimplementing XML-RPC framing) and records the method name and
// params plus the Basic-auth credentials seen, so a test can assert on all of
// them.
type rtorrentStub struct {
	requireUser, requirePass string // if requireUser is set, a mismatched pair gets a 401
	lastMethod               string
	lastParams               []any
	gotUser, gotPass         string
}

func newRTorrentStub(t *testing.T, s *rtorrentStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/RPC2", func(w http.ResponseWriter, r *http.Request) {
		s.gotUser, s.gotPass, _ = r.BasicAuth()
		if s.requireUser != "" && (s.gotUser != s.requireUser || s.gotPass != s.requirePass) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		name, params, _, err := xmlrpc.Unmarshal(r.Body)
		if err != nil {
			t.Errorf("unmarshal xmlrpc request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.lastMethod, s.lastParams = name, params

		var result any
		switch name {
		case "system.hostname":
			result = "rtorrent-host"
		case "load.start", "load.normal", "load.raw", "load.raw_start":
			result = 0
		default:
			t.Errorf("unexpected method %q", name)
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/xml")
		if err := xmlrpc.Marshal(w, "", result); err != nil {
			t.Errorf("marshal response: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestRTorrent(host, username, secret string, settings domain.RTorrentSettings) *rtorrentDriver {
	drv, err := newRTorrent(domain.DownloadClient{
		Host: host, Username: username, Settings: domain.DownloadClientSettings{RTorrent: &settings},
	}, secret, http.DefaultClient)
	if err != nil {
		panic(err)
	}
	return drv.(*rtorrentDriver)
}

func TestRTorrentTest_OK(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stub.lastMethod != "system.hostname" {
		t.Fatalf("lastMethod = %q, want system.hostname", stub.lastMethod)
	}
}

func TestRTorrentTest_BadAuth(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{requireUser: "admin", requirePass: "adminadmin"}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "wrong", domain.RTorrentSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for bad credentials")
	}
}

// rtorrentParamString extracts params[i] as a string, decoding a []byte param
// (rTorrent's URL/data args are always sent as base64 binary at the XML-RPC
// layer) to a string first.
func rtorrentParamString(t *testing.T, params []any, i int) string {
	t.Helper()
	if i >= len(params) {
		t.Fatalf("params has %d entries, want at least %d", len(params), i+1)
	}
	switch v := params[i].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		t.Fatalf("params[%d] = %T, want string or []byte", i, params[i])
		return ""
	}
}

func TestRTorrentAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{})

	url := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: url}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastMethod != "load.start" {
		t.Fatalf("lastMethod = %q, want load.start (not paused)", stub.lastMethod)
	}
	if got := rtorrentParamString(t, stub.lastParams, 1); got != url {
		t.Fatalf("params[1] = %q, want %q", got, url)
	}
}

func TestRTorrentAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{})

	raw := []byte("d8:announce...e")
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: raw}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastMethod != "load.raw_start" {
		t.Fatalf("lastMethod = %q, want load.raw_start (not paused)", stub.lastMethod)
	}
	if got := rtorrentParamString(t, stub.lastParams, 1); got != string(raw) {
		t.Fatalf("params[1] = %q, want %q", got, raw)
	}
}

func TestRTorrentAdd_Paused(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)

	t.Run("via URL, opts.Paused", func(t *testing.T) {
		drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{})
		if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{Paused: true}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if stub.lastMethod != "load.normal" {
			t.Fatalf("lastMethod = %q, want load.normal (paused via URL)", stub.lastMethod)
		}
	})

	t.Run("via bytes, settings.StartPaused", func(t *testing.T) {
		drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{StartPaused: true})
		if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte("x")}, AddOptions{}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if stub.lastMethod != "load.raw" {
			t.Fatalf("lastMethod = %q, want load.raw (paused via bytes)", stub.lastMethod)
		}
	})
}

func TestRTorrentAdd_LabelAndDirectory(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{Directory: "/downloads/rtorrent"})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{Category: "tv-sonarr"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(stub.lastParams) != 4 {
		t.Fatalf("params = %v, want 4 (\"\", data, label, directory)", stub.lastParams)
	}
	if got := rtorrentParamString(t, stub.lastParams, 2); got != `d.custom1.set="tv-sonarr"` {
		t.Fatalf("params[2] (label) = %q, want d.custom1.set=\"tv-sonarr\"", got)
	}
	if got := rtorrentParamString(t, stub.lastParams, 3); got != `d.directory.set="/downloads/rtorrent"` {
		t.Fatalf("params[3] (directory) = %q, want d.directory.set=\"/downloads/rtorrent\"", got)
	}
}

func TestRTorrentAdd_LabelFallbackToSettings(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{Label: "from-settings"})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := rtorrentParamString(t, stub.lastParams, 2); got != `d.custom1.set="from-settings"` {
		t.Fatalf("params[2] (label) = %q, want d.custom1.set=\"from-settings\"", got)
	}
}

func TestRTorrentAdd_BasicAuthCredentials(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.gotUser != "admin" || stub.gotPass != "adminadmin" {
		t.Fatalf("basic auth = %s/%s, want admin/adminadmin", stub.gotUser, stub.gotPass)
	}
}

// TestRTorrentAdd_RejectsQuoteInjection pins the guard against go-rtorrent's
// unescaped field.set="<value>" formatting: a '"' in any of the three
// candidate values (opts.Category, settings.Label, settings.Directory) must
// be rejected before a FieldValue is ever built, whichever one carries it.
func TestRTorrentAdd_RejectsQuoteInjection(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)

	tests := []struct {
		name     string
		opts     AddOptions
		settings domain.RTorrentSettings
	}{
		{"category", AddOptions{Category: `tv"; execute={rm,-rf,/}`}, domain.RTorrentSettings{}},
		{"settings label", AddOptions{}, domain.RTorrentSettings{Label: `tv"; execute={rm,-rf,/}`}},
		{"settings directory", AddOptions{}, domain.RTorrentSettings{Directory: `/downloads"; execute={rm,-rf,/}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", tt.settings)
			err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, tt.opts)
			if !errors.Is(err, errRTorrentFieldValue) {
				t.Fatalf("Add error = %v, want errRTorrentFieldValue", err)
			}
		})
	}
}

// TestNewRTorrent_TLSSkipVerify proves the InsecureSkipVerify transport clone
// actually takes effect: against a self-signed-cert server, a driver built
// without the setting fails TLS verification and one built with it succeeds —
// exercised end to end rather than by inspecting the unexported client fields.
func TestNewRTorrent_TLSSkipVerify(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/RPC2", func(w http.ResponseWriter, r *http.Request) {
		name, _, _, err := xmlrpc.Unmarshal(r.Body)
		if err != nil || name != "system.hostname" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_ = xmlrpc.Marshal(w, "", "rtorrent-host")
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	strict, err := newRTorrent(domain.DownloadClient{Host: srv.URL + "/RPC2"}, "", http.DefaultClient)
	if err != nil {
		t.Fatalf("newRTorrent (strict): %v", err)
	}
	if err := strict.Test(context.Background()); err == nil {
		t.Fatal("Test against a self-signed cert without TLSSkipVerify: expected a certificate error")
	}

	lenient, err := newRTorrent(domain.DownloadClient{
		Host:     srv.URL + "/RPC2",
		Settings: domain.DownloadClientSettings{RTorrent: &domain.RTorrentSettings{TLSSkipVerify: true}},
	}, "", http.DefaultClient)
	if err != nil {
		t.Fatalf("newRTorrent (lenient): %v", err)
	}
	if err := lenient.Test(context.Background()); err != nil {
		t.Fatalf("Test with TLSSkipVerify: %v", err)
	}
}

func TestRTorrentAdd_UsenetUnsupported(t *testing.T) {
	t.Parallel()
	stub := &rtorrentStub{}
	srv := newRTorrentStub(t, stub)
	drv := newTestRTorrent(srv.URL+"/RPC2", "admin", "adminadmin", domain.RTorrentSettings{})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(usenet) error = %v, want ErrUnsupportedProtocol", err)
	}
}
