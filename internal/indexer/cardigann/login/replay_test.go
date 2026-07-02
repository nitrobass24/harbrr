package login

import (
	"io"
	stdhttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// step is one recorded request/response pair in an ordered login sequence. The
// transport asserts the OUTGOING request matches wantMethod/wantPath and exposes
// captured request fields (form values, query, cookies) for the test to inspect
// WITHOUT the production code ever logging them.
type step struct {
	wantMethod string
	wantPath   string // URL path the request must hit, e.g. "/login.php"
	status     int
	respHeader stdhttp.Header
	bodyFile   string // file under testdata/ for the response body ("" => empty)
}

// captured records what the production code put on the wire for one step, so a
// test can assert "the CSRF token and credentials were posted" by reading the
// captured form — not by logging them.
type captured struct {
	method  string
	url     *url.URL
	form    url.Values
	query   url.Values
	cookies []*stdhttp.Cookie
	headers stdhttp.Header
}

// replayTransport is an offline Doer/RoundTripper driven by an ordered fixture
// of steps. It never touches the network. It records each request for later
// assertion and serves the canned response for that step.
type replayTransport struct {
	t     *testing.T
	steps []step

	mu       sync.Mutex
	idx      int
	captures []captured
}

func newReplay(t *testing.T, steps ...step) *replayTransport {
	t.Helper()
	return &replayTransport{t: t, steps: steps}
}

// Do satisfies the login.Doer seam.
func (r *replayTransport) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	return r.RoundTrip(req)
}

// RoundTrip satisfies http.RoundTripper so the same fixture can also back a real
// *http.Client (with a cookie jar) in tests that exercise the production seam.
func (r *replayTransport) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.idx >= len(r.steps) {
		r.t.Fatalf("replay: unexpected extra request %s %s", req.Method, req.URL.Path)
	}
	st := r.steps[r.idx]
	r.idx++

	r.record(req)

	if req.Method != st.wantMethod {
		r.t.Errorf("step %d: method = %s, want %s", r.idx-1, req.Method, st.wantMethod)
	}
	if req.URL.Path != st.wantPath {
		r.t.Errorf("step %d: path = %s, want %s", r.idx-1, req.URL.Path, st.wantPath)
	}

	return r.response(req, st), nil
}

// record captures the outgoing request, reading and re-buffering the body so the
// production code can still send it.
func (r *replayTransport) record(req *stdhttp.Request) {
	c := captured{
		method:  req.Method,
		url:     req.URL,
		query:   req.URL.Query(),
		cookies: req.Cookies(),
		headers: req.Header.Clone(),
	}
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if vals, err := url.ParseQuery(string(raw)); err == nil {
			c.form = vals
		}
	}
	r.captures = append(r.captures, c)
}

func (r *replayTransport) response(req *stdhttp.Request, st step) *stdhttp.Response {
	header := st.respHeader
	if header == nil {
		header = stdhttp.Header{}
	}
	var body string
	if st.bodyFile != "" {
		body = readFixture(r.t, st.bodyFile)
	}
	status := st.status
	if status == 0 {
		status = stdhttp.StatusOK
	}
	return &stdhttp.Response{
		StatusCode: status,
		Status:     strconv.Itoa(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// capture returns the recorded request at index i (0-based, in request order).
func (r *replayTransport) capture(i int) captured {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.captures) {
		r.t.Fatalf("replay: no captured request at index %d (have %d)", i, len(r.captures))
	}
	return r.captures[i]
}

func (r *replayTransport) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.captures)
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %q: %v", name, err)
	}
	return string(data)
}

// setCookieHeader builds a response Header carrying one Set-Cookie line.
func setCookieHeader(cookie string) stdhttp.Header {
	return stdhttp.Header{"Set-Cookie": {cookie}}
}
