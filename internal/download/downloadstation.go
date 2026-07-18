package download

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// dsAPIInfo is one entry of SYNO.API.Info's query response: the cgi path to call
// an API at, and the version range it supports.
type dsAPIInfo struct {
	Path       string `json:"path"`
	MinVersion int    `json:"minVersion"`
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

// queryInfo asks SYNO.API.Info which cgi path and version range SYNO.API.Auth and
// SYNO.DownloadStation2.Task are served at.
func (d *downloadStationDriver) queryInfo(ctx context.Context) (map[string]dsAPIInfo, error) {
	u := d.host + "/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.API.Auth,SYNO.DownloadStation2.Task"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("download: download-station: build info request: %w", apphttp.RedactURLError(err))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: download-station: info: %w", apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()

	var out dsInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("download: download-station: decode info: %w", err)
	}
	if !out.Success {
		return nil, errors.New("download: download-station: info query failed")
	}
	return out.Data, nil
}

// login authenticates for a DownloadStation session and returns the resulting
// _sid. The account/password ride in the request's query string — every error
// path routes through apphttp.RedactURLError, which drops the whole query (not
// just named params), so the password can never reach a returned error.
func (d *downloadStationDriver) login(ctx context.Context, authPath string, version int) (string, error) {
	q := url.Values{
		"api":     {"SYNO.API.Auth"},
		"version": {strconv.Itoa(version)},
		"method":  {"login"},
		"account": {d.username},
		"passwd":  {d.password},
		"session": {"DownloadStation"},
		"format":  {"sid"},
	}
	u := d.host + "/webapi/" + authPath + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("download: download-station: build login request: %w", apphttp.RedactURLError(err))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: download-station: login: %w", apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()

	var out dsLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("download: download-station: decode login: %w", err)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("download: download-station: build add request: %w", apphttp.RedactURLError(err))
	}
	return d.doCreate(req)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		return fmt.Errorf("download: download-station: build add request: %w", apphttp.RedactURLError(err))
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return d.doCreate(req)
}

// doCreate issues a create request and checks only success:true — DS's create
// response carries no task id worth reading (per #242, skip Prowlarr's
// re-list/id-matching).
func (d *downloadStationDriver) doCreate(req *http.Request) error {
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("download: download-station: add: %w", apphttp.RedactURLError(err))
	}
	defer resp.Body.Close()

	var out dsGenericResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("download: download-station: decode add response: %w", err)
	}
	if !out.Success {
		return errors.New("download: download-station: add: task creation failed")
	}
	return nil
}
