package template

import (
	"net/url"
	"testing"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return u
}

func TestNewDownloadURI(t *testing.T) {
	tests := []struct {
		name             string
		raw              string
		wantAbsoluteURI  string
		wantAbsolutePath string
		wantPathAndQuery string
		wantQuery        map[string]string
	}{
		{
			name:             "path and single query value",
			raw:              "https://example.org/download/info/42?id=42",
			wantAbsoluteURI:  "https://example.org/download/info/42?id=42",
			wantAbsolutePath: "/download/info/42",
			wantPathAndQuery: "/download/info/42?id=42",
			wantQuery:        map[string]string{"id": "42"},
		},
		{
			name:             "empty path normalizes to root like .NET AbsolutePath",
			raw:              "https://example.org?id=7",
			wantAbsoluteURI:  "https://example.org?id=7",
			wantAbsolutePath: "/",
			wantPathAndQuery: "/?id=7",
			wantQuery:        map[string]string{"id": "7"},
		},
		{
			name:             "escaped path and multiple keys",
			raw:              "https://t.example/a%20b/c?x=1&y=two",
			wantAbsoluteURI:  "https://t.example/a%20b/c?x=1&y=two",
			wantAbsolutePath: "/a%20b/c",
			wantPathAndQuery: "/a%20b/c?x=1&y=two",
			wantQuery:        map[string]string{"x": "1", "y": "two"},
		},
		{
			name:             "no query",
			raw:              "https://example.org/t/1",
			wantAbsoluteURI:  "https://example.org/t/1",
			wantAbsolutePath: "/t/1",
			wantPathAndQuery: "/t/1",
			wantQuery:        map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			du := NewDownloadURI(mustParse(t, tt.raw))
			if du.AbsoluteUri != tt.wantAbsoluteURI {
				t.Errorf("AbsoluteUri = %q, want %q", du.AbsoluteUri, tt.wantAbsoluteURI)
			}
			if du.AbsolutePath != tt.wantAbsolutePath {
				t.Errorf("AbsolutePath = %q, want %q", du.AbsolutePath, tt.wantAbsolutePath)
			}
			if du.PathAndQuery != tt.wantPathAndQuery {
				t.Errorf("PathAndQuery = %q, want %q", du.PathAndQuery, tt.wantPathAndQuery)
			}
			if len(du.Query) != len(tt.wantQuery) {
				t.Fatalf("Query = %v, want %v", du.Query, tt.wantQuery)
			}
			for k, want := range tt.wantQuery {
				if du.Query[k] != want {
					t.Errorf("Query[%q] = %q, want %q", k, du.Query[k], want)
				}
			}
		})
	}
}

// TestEvalDownloadURI exercises the namespace through both evaluation paths: the
// stdlib parser (bare interpolation, resolved by Go field name) and the
// re_replace pre-pass (resolveStringVar -> resolveDownloadURIVar).
func TestEvalDownloadURI(t *testing.T) {
	ctx := NewContext()
	ctx.DownloadUri = NewDownloadURI(mustParse(t, "https://example.org/download/info/42?id=42&sub=x"))

	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{name: "absolute uri", tmpl: "{{ .DownloadUri.AbsoluteUri }}", want: "https://example.org/download/info/42?id=42&sub=x"},
		{name: "query member", tmpl: "{{ .DownloadUri.Query.id }}", want: "42"},
		{name: "absolute path", tmpl: "{{ .DownloadUri.AbsolutePath }}", want: "/download/info/42"},
		{name: "path and query", tmpl: "{{ .DownloadUri.PathAndQuery }}", want: "/download/info/42?id=42&sub=x"},
		{name: "re_replace pre-pass on absolute path", tmpl: `{{ re_replace .DownloadUri.AbsolutePath "/info/" "/" }}`, want: "/download/42"},
		{name: "query member in a path", tmpl: "/dl/{{ .DownloadUri.Query.id }}.torrent", want: "/dl/42.torrent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Eval(tt.tmpl, ctx)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tt.tmpl, err)
			}
			if got != tt.want {
				t.Errorf("Eval(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

// TestEvalDownloadURINilGuard confirms the re_replace pre-pass yields "" when
// DownloadUri is unpopulated (resolveDownloadURIVar's nil-guard), matching
// Jackett's missingkey=zero. Bare {{ .DownloadUri.X }} with a nil pointer is a
// documented hard error and intentionally not exercised here.
func TestEvalDownloadURINilGuard(t *testing.T) {
	ctx := NewContext()
	got, err := Eval(`{{ re_replace .DownloadUri.AbsolutePath "/info/" "/" }}`, ctx)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != "" {
		t.Errorf("nil DownloadUri pre-pass = %q, want empty", got)
	}
}
