// Copyright (c) 2021 - 2025, Ludvig Lundgren and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sabnzbd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	addr   string
	apiKey string

	http *http.Client
}

type Options struct {
	Addr   string
	ApiKey string //nolint:revive // var-naming: matches upstream autobrr pkg/sabnzbd's Options.ApiKey verbatim (#241 byte-identical port).

	// HTTPClient, when set, is used instead of the package default (harbrr injects
	// its shared *http.Client here rather than porting pkg/sharedhttp's Transport).
	HTTPClient *http.Client
}

func New(opts Options) *Client {
	c := &Client{
		addr:   opts.Addr,
		apiKey: opts.ApiKey,
		http: &http.Client{
			Timeout: time.Second * 60,
		},
	}

	if opts.HTTPClient != nil {
		c.http = opts.HTTPClient
	}

	return c
}

func (c *Client) AddFromUrl(ctx context.Context, r AddNzbRequest) (*AddFileResponse, error) { //nolint:revive // var-naming: matches upstream autobrr pkg/sabnzbd's AddFromUrl verbatim (#241 byte-identical port).
	v := url.Values{}
	v.Set("mode", "addurl")
	v.Set("name", r.Url)
	v.Set("output", "json")
	v.Set("apikey", c.apiKey)
	v.Set("cat", "*")

	if r.Category != "" {
		v.Set("cat", r.Category)
	}

	var data AddFileResponse
	if err := c.call(ctx, http.MethodGet, v, nil, "", &data); err != nil {
		return nil, err
	}

	return &data, nil
}

// AddFile uploads the nzb BYTES to SABnzbd (mode=addfile, multipart) instead of
// handing it a URL. Not part of the upstream autobrr port (which is add-by-URL only):
// harbrr needs it because a sealed harbrr download link is only fetchable by harbrr
// itself, so the .nzb has to be uploaded. Written in AddFromUrl's exact style — same
// /api endpoint, same apikey/output/cat query params, same body handling.
func (c *Client) AddFile(ctx context.Context, r AddNzbFileRequest) (*AddFileResponse, error) {
	v := url.Values{}
	v.Set("mode", "addfile")
	v.Set("output", "json")
	v.Set("apikey", c.apiKey)
	v.Set("cat", "*")

	if r.Category != "" {
		v.Set("cat", r.Category)
	}

	body, contentType, err := nzbMultipart(r.Filename, r.Nzb)
	if err != nil {
		return nil, err
	}

	var data AddFileResponse
	if err := c.call(ctx, http.MethodPost, v, body, contentType, &data); err != nil {
		return nil, err
	}

	return &data, nil
}

func (c *Client) Version(ctx context.Context) (*VersionResponse, error) {
	v := url.Values{}
	v.Set("mode", "version")
	v.Set("output", "json")
	v.Set("apikey", c.apiKey)

	var data VersionResponse
	if err := c.call(ctx, http.MethodGet, v, nil, "", &data); err != nil {
		return nil, err
	}

	return &data, nil
}

// call issues one request against SABnzbd's single /api endpoint — v carries the mode
// and every other parameter — and decodes the JSON response into out. body/contentType
// are nil/"" for the GET modes; only mode=addfile posts a body. Every caller shares
// this one request/decode cycle, so the URL shape and error wording cannot drift per
// mode.
//
// The response is decoded straight off res.Body: json.Decoder already reports an empty
// body as io.EOF, so no read-ahead is needed to tell "nothing came back" apart from
// "not JSON". Errors are deliberately unwrapped (upstream's shape — see the wrapcheck
// exclusion in .golangci.yml); internal/download/sabnzbd.go is the layer that redacts
// and prefixes anything this package returns.
func (c *Client) call(ctx context.Context, method string, v url.Values, body io.Reader, contentType string, out any) error {
	addr, err := url.JoinPath(c.addr, "/api")
	if err != nil {
		return err
	}

	u, err := url.Parse(addr)
	if err != nil {
		return err
	}

	u.RawQuery = v.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}

	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("could not unmarshal body: %w", err)
	}

	return nil
}

// nzbMultipart builds the mode=addfile body: a single "name" file part carrying the
// nzb bytes, which is what SABnzbd reads the upload from.
func nzbMultipart(filename string, nzb []byte) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("name", filename)
	if err != nil {
		return nil, "", fmt.Errorf("could not build multipart body: %w", err)
	}
	if _, err := part.Write(nzb); err != nil {
		return nil, "", fmt.Errorf("could not write nzb part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("could not close multipart body: %w", err)
	}
	return &buf, mw.FormDataContentType(), nil
}

type VersionResponse struct {
	Version string `json:"version"`
}

type AddFileResponse struct {
	NzoIDs []string `json:"nzo_ids"`
	ApiError
}

type ApiError struct { //nolint:revive // var-naming: matches upstream autobrr pkg/sabnzbd's ApiError verbatim (#241 byte-identical port).
	ErrorMsg string `json:"error,omitempty"`
}

type AddNzbRequest struct {
	Url      string //nolint:revive // var-naming: matches upstream autobrr pkg/sabnzbd's AddNzbRequest.Url verbatim (#241 byte-identical port).
	Category string
}

// AddNzbFileRequest is AddFile's input: the nzb bytes plus the filename SABnzbd names
// the job after. harbrr-only (no upstream counterpart).
type AddNzbFileRequest struct {
	Filename string
	Nzb      []byte
	Category string
}
