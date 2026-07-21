package httpx

import (
	stdhttp "net/http"
	"testing"
)

func TestIsRedirectStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{"301 moved permanently", stdhttp.StatusMovedPermanently, true},
		{"302 found", stdhttp.StatusFound, true},
		{"303 see other", stdhttp.StatusSeeOther, true},
		{"307 temporary redirect", stdhttp.StatusTemporaryRedirect, true},
		{"308 permanent redirect", stdhttp.StatusPermanentRedirect, true},
		{"200 OK", stdhttp.StatusOK, false},
		{"204 no content", stdhttp.StatusNoContent, false},
		// 304 Not Modified is a Location-less cache-revalidation response, not a
		// redirect to follow — it must stay OUT of the set despite being a 3xx.
		{"304 not modified", stdhttp.StatusNotModified, false},
		{"400 bad request", stdhttp.StatusBadRequest, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRedirectStatus(tt.status); got != tt.want {
				t.Errorf("IsRedirectStatus(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestResolveLocation(t *testing.T) {
	tests := []struct {
		name   string
		status int
		reqURL string
		locHdr string
		want   string
	}{
		{
			name:   "absolute location",
			status: stdhttp.StatusFound,
			reqURL: "https://tracker.example/login",
			locHdr: "https://tracker.example/account",
			want:   "https://tracker.example/account",
		},
		{
			name:   "relative location resolves against request URL",
			status: stdhttp.StatusFound,
			reqURL: "https://tracker.example/a/b?x=1",
			locHdr: "../c",
			want:   "https://tracker.example/c",
		},
		{
			name:   "missing location on a redirect status",
			status: stdhttp.StatusFound,
			reqURL: "https://tracker.example/login",
			locHdr: "",
			want:   "",
		},
		{
			name:   "non-3xx status ignores any location header",
			status: stdhttp.StatusOK,
			reqURL: "https://tracker.example/login",
			locHdr: "https://tracker.example/account",
			want:   "",
		},
		{
			name:   "304 not modified is not a redirect",
			status: stdhttp.StatusNotModified,
			reqURL: "https://tracker.example/login",
			locHdr: "https://tracker.example/account",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &stdhttp.Response{StatusCode: tt.status, Header: stdhttp.Header{}}
			if tt.locHdr != "" {
				resp.Header.Set("Location", tt.locHdr)
			}
			if got := ResolveLocation(resp, tt.reqURL); got != tt.want {
				t.Errorf("ResolveLocation() = %q, want %q", got, tt.want)
			}
		})
	}
}
