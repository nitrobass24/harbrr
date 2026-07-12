package nzbindex

import (
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
)

// TestGrab proves a .nzb download returns the body as an application/x-nzb GrabResult with no
// Redirect (an .nzb is always fetched, never redirected).
func TestGrab(t *testing.T) {
	t.Parallel()
	const nzb = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"></nzb>`
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response {
		return &stdhttp.Response{
			StatusCode: stdhttp.StatusOK,
			Header:     stdhttp.Header{"Content-Type": {"application/x-nzb"}},
			Body:       io.NopCloser(strings.NewReader(nzb)),
		}
	}}
	d := testDriver(t, nil, doer)
	res, err := d.Grab(t.Context(), testBaseURL+"/api/download/abc.nzb")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if res.ContentType != nzbContentType {
		t.Errorf("ContentType = %q, want %q", res.ContentType, nzbContentType)
	}
	if string(res.Body) != nzb {
		t.Errorf("Body = %q, want the nzb bytes", res.Body)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (nzb is always a body)", res.Redirect)
	}
}

// TestGrabNon200 proves a non-2xx download surfaces an error rather than serving a body.
func TestGrabNon200(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response { return statusResponse(stdhttp.StatusNotFound) }}
	d := testDriver(t, nil, doer)
	if _, err := d.Grab(t.Context(), testBaseURL+"/api/download/missing.nzb"); err == nil {
		t.Fatal("want an error for a 404 download")
	}
}
