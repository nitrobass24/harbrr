package speedapp

import (
	"bytes"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	testEmail    = "retroflix-test@example.invalid"
	testPassword = "RETROFLIX-SYNTHETIC-PASSWORD"
	testToken    = "RETROFLIX-SYNTHETIC-TOKEN-ONE"
	nextToken    = "RETROFLIX-SYNTHETIC-TOKEN-TWO"
	torrentBytes = "d8:announce15:tracker.invalid4:infod6:lengthi1eee"
)

type recordedRequest struct {
	method        string
	url           string
	path          string
	rawQuery      string
	authorization string
	accept        string
	contentType   string
	body          string
}

type scriptDoer struct {
	mu       sync.Mutex
	requests []recordedRequest
	handler  func(*stdhttp.Request, []byte) (*stdhttp.Response, error)
}

func (d *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	record := recordedRequest{
		method:        req.Method,
		url:           req.URL.String(),
		path:          req.URL.Path,
		rawQuery:      req.URL.RawQuery,
		authorization: req.Header.Get("Authorization"),
		accept:        req.Header.Get("Accept"),
		contentType:   req.Header.Get("Content-Type"),
		body:          string(body),
	}
	d.mu.Lock()
	d.requests = append(d.requests, record)
	d.mu.Unlock()
	if d.handler == nil {
		return jsonResponse(stdhttp.StatusOK, `[]`), nil
	}
	return d.handler(req, body)
}

func (d *scriptDoer) records() []recordedRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]recordedRequest(nil), d.requests...)
}

func rawResponse(status int, contentType string, body io.Reader) *stdhttp.Response {
	header := stdhttp.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &stdhttp.Response{StatusCode: status, Header: header, Body: io.NopCloser(body)}
}

func stringResponse(status int, contentType, body string) *stdhttp.Response {
	return rawResponse(status, contentType, bytes.NewBufferString(body))
}

func jsonResponse(status int, body string) *stdhttp.Response {
	return stringResponse(status, "application/json", body)
}

func torrentResponse(status int, body string) *stdhttp.Response {
	return stringResponse(status, "application/x-bittorrent", body)
}

func testDriver(t *testing.T, doer search.Doer) *driver {
	t.Helper()
	built, err := New(native.Params{
		Def:  Definition(),
		Cfg:  map[string]string{"email": testEmail, "password": testPassword},
		Doer: doer,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.(*driver)
}

func seedDriverToken(d *driver, value string, generation uint64) {
	d.authMu.Lock()
	d.token = tokenVersion{value: value, generation: generation}
	d.authMu.Unlock()
}

func testRow(id int64, title, description string) apiRow {
	return apiRow{
		ID:                   id,
		URL:                  "https://retroflix.club/torrent/" + stringID(id),
		Name:                 title,
		ShortDescription:     description,
		Size:                 1024 + id,
		CreatedAt:            "2026-08-17T10:15:30Z",
		TimesCompleted:       id,
		Seeders:              10,
		Leechers:             2,
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
		Category:             apiCategory{ID: 401, Name: "Movies"},
	}
}

func stringID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func rowsJSON(t *testing.T, rows ...apiRow) string {
	t.Helper()
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	return string(body)
}
