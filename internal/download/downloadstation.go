package download

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// downloadStationDriver is a thin HTTP client for Synology Download Station's V2
// API (SYNO.DownloadStation2.Task). No session persistence across calls — Test
// and Add each run a fresh path-discovery + login, mirroring the qBittorrent
// driver's per-call LoginCtx precedent. V1 fallback and 2FA are deliberately
// unsupported (modern DSM has V2; 2FA login is out of scope for #242).
type downloadStationDriver struct {
	host      string
	username  string
	password  string
	directory string
	client    *http.Client
}

// newDownloadStation builds the driver from a configured client row and its
// decrypted secret (the account password).
func newDownloadStation(c domain.DownloadClient, secret string, client *http.Client) (Driver, error) {
	var settings domain.DownloadStationSettings
	if c.Settings.DownloadStation != nil {
		settings = *c.Settings.DownloadStation
	}
	return &downloadStationDriver{
		host:      strings.TrimRight(c.Host, "/"),
		username:  c.Username,
		password:  secret,
		directory: strings.TrimPrefix(settings.Directory, "/"),
		client:    client,
	}, nil
}

// dsAPIInfo is one entry of SYNO.API.Info's query response: the cgi path to call an
// API at, and the highest version it supports (the advertised minVersion is never
// read — harbrr pins the version it sends off maxVersion alone).
type dsAPIInfo struct {
	Path       string `json:"path"`
	MaxVersion int    `json:"maxVersion"`
}

type dsInfoResponse struct {
	Success bool                 `json:"success"`
	Data    map[string]dsAPIInfo `json:"data"`
}

type dsLoginResponse struct {
	Success bool `json:"success"`
	Data    struct {
		SID string `json:"sid"`
	} `json:"data"`
}

type dsGenericResponse struct {
	Success bool `json:"success"`
}

// dsSession is the outcome of authenticate: the cgi path SYNO.DownloadStation2.Task
// is served from, and the session id every subsequent call carries as a query param.
type dsSession struct {
	taskPath string
	sid      string
}

// Test runs discovery + login: a valid SID and an advertised
// SYNO.DownloadStation2.Task v2 together prove the client is reachable and
// configured correctly.
func (d *downloadStationDriver) Test(ctx context.Context) error {
	_, err := d.authenticate(ctx)
	return err
}

// Add creates a download task for a torrent or nzb payload (DS's create call is
// protocol-agnostic — the same endpoint takes either). opts.Category, if set,
// overrides the configured directory (DS's only foldering concept); opts.Tags and
// opts.Paused have no DS equivalent and are ignored. The create response carries
// no task id worth reading (per #242) — success:true is the only check.
func (d *downloadStationDriver) Add(ctx context.Context, p Payload, opts AddOptions) error {
	if p.Protocol != ProtocolTorrent && p.Protocol != ProtocolUsenet {
		return fmt.Errorf("download: download-station: %w: %s", ErrUnsupportedProtocol, p.Protocol)
	}
	sess, err := d.authenticate(ctx)
	if err != nil {
		return err
	}
	destination := d.directory
	if opts.Category != "" {
		destination = strings.TrimPrefix(opts.Category, "/")
	}

	base := d.host + "/webapi/" + sess.taskPath
	if len(p.Bytes) > 0 {
		return d.addFile(ctx, base, sess.sid, destination, p)
	}
	return d.addURL(ctx, base, sess.sid, destination, p.URL)
}

// authenticate discovers the SYNO.API.Auth and SYNO.DownloadStation2.Task cgi
// paths, picks a login API version from SYNO.API.Auth's advertised maxVersion
// (6 when >= 7, else 2), and logs in for a DownloadStation session.
func (d *downloadStationDriver) authenticate(ctx context.Context) (dsSession, error) {
	info, err := d.queryInfo(ctx)
	if err != nil {
		return dsSession{}, err
	}
	auth, ok := info["SYNO.API.Auth"]
	if !ok {
		return dsSession{}, errors.New("download: download-station: SYNO.API.Auth not advertised")
	}
	task, ok := info["SYNO.DownloadStation2.Task"]
	if !ok || task.MaxVersion < 2 {
		return dsSession{}, errors.New("download: download-station: SYNO.DownloadStation2.Task v2 not supported")
	}
	authVersion := 2
	if auth.MaxVersion >= 7 {
		authVersion = 6
	}
	sid, err := d.login(ctx, auth.Path, authVersion)
	if err != nil {
		return dsSession{}, err
	}
	return dsSession{taskPath: task.Path, sid: sid}, nil
}

// call issues one Synology webapi request and decodes the JSON response into out. step
// names the call in every error ("info", "login", "add"); body/contentType are nil/""
// for the plain GETs. Every DS call shares this one request/decode cycle, so the
// redaction cannot drift between them.
//
// Errors route through apphttp.RedactURLError rather than the shared JSONClient: DS
// carries the session id, the destination directory and — on the type=url create — the
// passkey-bearing release link in the request's QUERY STRING, and JSONClient formats
// the request path verbatim into every error it emits. RedactURLError keeps
// scheme://host and drops the rest.
func (d *downloadStationDriver) call(ctx context.Context, method, u, step, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("download: download-station: build %s request: %w", step, apphttp.RedactURLError(err))
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("download: download-station: %s: %w", step, apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("download: download-station: decode %s: %w", step, err)
	}
	return nil
}

// queryInfo asks SYNO.API.Info which cgi path and version range SYNO.API.Auth and
// SYNO.DownloadStation2.Task are served at.
func (d *downloadStationDriver) queryInfo(ctx context.Context) (map[string]dsAPIInfo, error) {
	u := d.host + "/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.API.Auth,SYNO.DownloadStation2.Task"
	var out dsInfoResponse
	if err := d.call(ctx, http.MethodGet, u, "info", "", nil, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, errors.New("download: download-station: info query failed")
	}
	return out.Data, nil
}

// login authenticates for a DownloadStation session and returns the resulting
// _sid. account/passwd ride in a POST form body, not the URL — SYNO.API.Auth
// accepts application/x-www-form-urlencoded — so the credentials never appear on
// the wire path a proxy/access log records (the request line + query string),
// unlike the GET-with-query-params shape every other DS call uses. Every error
// path still routes through apphttp.RedactURLError as a second line of defense.
func (d *downloadStationDriver) login(ctx context.Context, authPath string, version int) (string, error) {
	form := url.Values{
		"api":     {"SYNO.API.Auth"},
		"version": {strconv.Itoa(version)},
		"method":  {"login"},
		"account": {d.username},
		"passwd":  {d.password},
		"session": {"DownloadStation"},
		"format":  {"sid"},
	}
	u := d.host + "/webapi/" + authPath
	var out dsLoginResponse
	err := d.call(ctx, http.MethodPost, u, "login", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), &out)
	if err != nil {
		return "", err
	}
	if !out.Success || out.Data.SID == "" {
		return "", errors.New("download: download-station: login failed")
	}
	return out.Data.SID, nil
}

// addURL creates a task from a magnet/http/nzb URL DS fetches itself.
func (d *downloadStationDriver) addURL(ctx context.Context, base, sid, destination, target string) error {
	q := url.Values{
		"api":         {"SYNO.DownloadStation2.Task"},
		"version":     {"2"},
		"method":      {"create"},
		"type":        {"url"},
		"url":         {target},
		"create_list": {"false"},
		"_sid":        {sid},
	}
	if destination != "" {
		q.Set("destination", destination)
	}
	return d.create(ctx, http.MethodGet, base+"?"+q.Encode(), "", nil)
}

// addFile creates a task from fetched bytes, uploaded as multipart/form-data.
// Synology's file-upload convention: the "file" field's VALUE is a JSON-quoted
// array naming the file part(s) ([]string{"fileData"}), and the actual bytes go
// in a part named "fileData".
func (d *downloadStationDriver) addFile(ctx context.Context, base, sid, destination string, p Payload) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := map[string]string{
		"api":         "SYNO.DownloadStation2.Task",
		"version":     "2",
		"method":      "create",
		"type":        "file",
		"file":        `["fileData"]`,
		"create_list": "false",
	}
	if destination != "" {
		fields["destination"] = destination
	}
	for name, value := range fields {
		if err := mw.WriteField(name, value); err != nil {
			return fmt.Errorf("download: download-station: build multipart: %w", err)
		}
	}
	fw, err := mw.CreateFormFile("fileData", cmp.Or(p.Name, "upload"))
	if err != nil {
		return fmt.Errorf("download: download-station: build multipart: %w", err)
	}
	if _, err := fw.Write(p.Bytes); err != nil {
		return fmt.Errorf("download: download-station: build multipart: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("download: download-station: build multipart: %w", err)
	}

	u := base + "?_sid=" + url.QueryEscape(sid)
	return d.create(ctx, http.MethodPost, u, mw.FormDataContentType(), &body)
}

// create issues a create request and checks only success:true — DS's create
// response carries no task id worth reading (per #242, skip Prowlarr's
// re-list/id-matching).
func (d *downloadStationDriver) create(ctx context.Context, method, u, contentType string, body io.Reader) error {
	var out dsGenericResponse
	if err := d.call(ctx, method, u, "add", contentType, body, &out); err != nil {
		return err
	}
	if !out.Success {
		return errors.New("download: download-station: add: task creation failed")
	}
	return nil
}
