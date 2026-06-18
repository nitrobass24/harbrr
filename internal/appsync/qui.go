package appsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// autobrr/qui registers Torznab endpoints as "native" indexers it searches. Its
// contract is a third dialect: flat snake_case JSON (not Sonarr's fields[] envelope),
// header X-API-Key, and per-indexer category objects. harbrr pushes its feed as a
// native indexer whose base_url is the complete per-slug feed URL.
const (
	quiBackendNative = "native"
	quiIndexersPath  = "/api/torznab/indexers"
	quiErrBodyLimit  = 2048
)

// quiCategory is one entry of qui's per-indexer categories[].
type quiCategory struct {
	CategoryID   int    `json:"category_id"`
	CategoryName string `json:"category_name"`
}

// quiIndexer is qui's Torznab indexer resource (the subset harbrr sets/reads). ID is
// assigned by qui; api_key is write-only (qui never echoes it back).
type quiIndexer struct {
	ID         int           `json:"id,omitempty"`
	Name       string        `json:"name"`
	BaseURL    string        `json:"base_url"`
	APIKey     string        `json:"api_key,omitempty"`
	Backend    string        `json:"backend"`
	Enabled    bool          `json:"enabled"`
	Priority   int           `json:"priority"`
	Categories []quiCategory `json:"categories"`
}

// quiDriver implements Target for an autobrr/qui instance.
type quiDriver struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

var _ Target = (*quiDriver)(nil)

// NewQui builds a Target for a qui instance. baseURL is qui's own origin; apiKey is
// its API key (header X-API-Key).
func NewQui(baseURL, apiKey string, client *http.Client) Target {
	if client == nil {
		client = http.DefaultClient
	}
	return &quiDriver{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client}
}

func (q *quiDriver) Kind() string { return domain.AppKindQui }

// buildIndexer maps a DesiredIndexer to qui's native-indexer body. Pure (no I/O) so
// the golden freezes the snake_case field mapping.
func (q *quiDriver) buildIndexer(d DesiredIndexer) quiIndexer {
	cats := make([]quiCategory, 0, len(d.Categories))
	for _, c := range d.Categories {
		cats = append(cats, quiCategory{CategoryID: c.ID, CategoryName: c.Name})
	}
	return quiIndexer{
		Name: d.Name, BaseURL: d.FeedURL, APIKey: d.APIKey,
		Backend: quiBackendNative, Enabled: d.Enabled, Priority: d.Priority,
		Categories: cats,
	}
}

func (q *quiDriver) List(ctx context.Context) ([]RemoteIndexer, error) {
	var raw []quiIndexer
	if _, err := q.do(ctx, http.MethodGet, quiIndexersPath, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]RemoteIndexer, 0, len(raw))
	for _, r := range raw {
		out = append(out, RemoteIndexer{
			RemoteID: strconv.Itoa(r.ID), Name: r.Name,
			FeedURL: r.BaseURL, ManagedBySlug: slugFromFeedURL(r.BaseURL),
		})
	}
	return out, nil
}

func (q *quiDriver) Create(ctx context.Context, d DesiredIndexer) (string, error) {
	var resp quiIndexer
	if _, err := q.do(ctx, http.MethodPost, quiIndexersPath, q.buildIndexer(d), &resp); err != nil {
		return "", err
	}
	return strconv.Itoa(resp.ID), nil
}

func (q *quiDriver) Update(ctx context.Context, remoteID string, d DesiredIndexer) error {
	_, err := q.do(ctx, http.MethodPut, quiIndexersPath+"/"+remoteID, q.buildIndexer(d), nil)
	return err
}

func (q *quiDriver) Delete(ctx context.Context, remoteID string) error {
	_, err := q.do(ctx, http.MethodDelete, quiIndexersPath+"/"+remoteID, nil, nil)
	return err
}

// Test validates a configured indexer by id (qui tests stored indexers, not a posted
// body). The reconciler's connection-level Test path resolves the remote id first; a
// DesiredIndexer with no known remote id cannot be tested in isolation, so Create is
// the effective probe. Here Test is best-effort against an existing row.
func (q *quiDriver) Test(ctx context.Context, d DesiredIndexer) error {
	remote, err := q.List(ctx)
	if err != nil {
		return err
	}
	for _, r := range remote {
		if r.ManagedBySlug == d.Slug {
			_, err := q.do(ctx, http.MethodPost, quiIndexersPath+"/"+r.RemoteID+"/test", nil, nil)
			return err
		}
	}
	return fmt.Errorf("appsync: qui: no indexer for slug %q to test", d.Slug)
}

// do performs one authenticated request. The request body carries the harbrr feed key
// but is never echoed into an error; the X-API-Key header is never logged.
func (q *quiDriver) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("appsync: qui: marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, q.baseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("appsync: qui: build request: %w", err)
	}
	req.Header.Set("X-API-Key", q.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("appsync: qui: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, quiErrBodyLimit))
		msg := apphttp.RedactError(errors.New(strings.TrimSpace(string(snippet))))
		return resp.StatusCode, fmt.Errorf("appsync: qui: %s %s: status %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("appsync: qui: decode %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}
