// Copyright (c) 2021 - 2025, Ludvig Lundgren and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nzbget

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	host     string
	username string
	password string

	log *log.Logger

	http *http.Client
}

type Options struct {
	Host     string
	Username string
	Password string

	Log *log.Logger

	// HTTPClient, when set, is used instead of the package default (harbrr injects
	// its shared *http.Client here rather than porting pkg/sharedhttp's Transport).
	HTTPClient *http.Client
}

func New(opts Options) *Client {
	c := &Client{
		host:     opts.Host,
		username: opts.Username,
		password: opts.Password,
		log:      log.New(io.Discard, "", log.LstdFlags),
		http: &http.Client{
			Timeout: time.Second * 60,
		},
	}

	if opts.Log != nil {
		c.log = opts.Log
	}

	if opts.HTTPClient != nil {
		c.http = opts.HTTPClient
	}

	return c
}

type rpcRequest struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
	ID     int           `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) call(ctx context.Context, method string, params []interface{}, result interface{}) error {
	body, err := json.Marshal(rpcRequest{
		Method: method,
		Params: params,
		ID:     1,
	})
	if err != nil {
		return fmt.Errorf("could not marshal rpc request: %w", err)
	}

	endpoint, err := url.JoinPath(c.host, "/jsonrpc")
	if err != nil {
		return fmt.Errorf("could not build nzbget endpoint url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("could not build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not make request to nzbget: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("nzbget returned status %d", res.StatusCode)
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(res.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("could not decode rpc response: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("nzbget rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if result != nil {
		if err := json.Unmarshal(rpcResp.Result, result); err != nil {
			return fmt.Errorf("could not unmarshal rpc result: %w", err)
		}
	}

	return nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var version string
	if err := c.call(ctx, "version", nil, &version); err != nil {
		return "", err
	}
	return version, nil
}

type AddNzbRequest struct {
	URL      string
	Category string
}

type AddNzbResponse struct {
	NzbID int
}

func (c *Client) AddFromURL(ctx context.Context, r AddNzbRequest) (*AddNzbResponse, error) {
	// NZBGet append params: Filename, URL, Category, Priority, AddToTop,
	// AddPaused, DupeKey, DupeScore, DupeMode, PPParameters
	params := []interface{}{
		"",         // Filename
		r.URL,      // URL
		r.Category, // Category
		0,          // Priority
		false,      // AddToTop
		false,      // AddPaused
		"",         // DupeKey
		0,          // DupeScore
		"SCORE",    // DupeMode
		[]string{}, // PPParameters
	}

	var nzbID int
	if err := c.call(ctx, "append", params, &nzbID); err != nil {
		return nil, fmt.Errorf("could not add nzb to nzbget: %w", err)
	}

	if nzbID <= 0 {
		return nil, fmt.Errorf("nzbget returned invalid nzb id: %d", nzbID)
	}

	return &AddNzbResponse{NzbID: nzbID}, nil
}
