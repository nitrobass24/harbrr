package appsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Servarr (Sonarr/Radarr) v3 Torznab-indexer contract. Both apps share the same
// REST shape; the only behavioral difference is that Sonarr carries anime
// categories. A harbrr indexer maps to one Torznab indexer whose baseUrl is the
// complete per-slug feed URL and whose apiPath is empty (the path is already whole —
// the C1 correction; an "/api" apiPath would make every indexer's test fail).
const (
	servarrImplementation = "Torznab"
	servarrConfigContract = "TorznabSettings"
	servarrProtocol       = "torrent"
	servarrIndexerPath    = "/api/v3/indexer"
	newznabAnimeCategory  = 5070
)

// servarrField is one entry of a Servarr indexer's fields array. The value is
// heterogeneous on the wire (string, int array, bool), so it is carried as raw JSON
// rather than a bare any — built typed via field().
type servarrField struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// servarrIndexer is the Servarr v3 IndexerResource (the subset harbrr sets). ID is
// omitted on create and set on update.
type servarrIndexer struct {
	ID                      int            `json:"id,omitempty"`
	Name                    string         `json:"name"`
	Implementation          string         `json:"implementation"`
	ImplementationName      string         `json:"implementationName"`
	ConfigContract          string         `json:"configContract"`
	Protocol                string         `json:"protocol"`
	EnableRss               bool           `json:"enableRss"`
	EnableAutomaticSearch   bool           `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool           `json:"enableInteractiveSearch"`
	Priority                int            `json:"priority"`
	Fields                  []servarrField `json:"fields"`
	Tags                    []int          `json:"tags"`
}

// servarrDriver implements Target for a Sonarr or Radarr instance.
type servarrDriver struct {
	kind    string
	baseURL string
	apiKey  string
	client  *http.Client
	anime   bool
}

var _ Target = (*servarrDriver)(nil)

// newServarr builds a Servarr driver. apiKey is the *app's* key (to authenticate to
// it); the harbrr feed key travels inside each pushed indexer body.
func newServarr(kind, baseURL, apiKey string, client *http.Client, anime bool) *servarrDriver {
	if client == nil {
		client = defaultHTTPClient()
	}
	return &servarrDriver{
		kind: kind, baseURL: strings.TrimRight(baseURL, "/"),
		apiKey: apiKey, client: client, anime: anime,
	}
}

// buildIndexer marshals a DesiredIndexer into the Servarr resource. Pure (no I/O) so
// the golden test freezes the exact field mapping.
func (s *servarrDriver) buildIndexer(d DesiredIndexer) servarrIndexer {
	ids := d.CategoryIDs()
	fields := []servarrField{
		field("baseUrl", d.FeedURL),
		field("apiPath", ""),
		field("apiKey", d.APIKey),
		field("categories", intsOrEmpty(ids)),
	}
	if s.anime {
		fields = append(fields, field("animeCategories", animeCats(ids)))
	}
	return servarrIndexer{
		Name:                    d.Name,
		Implementation:          servarrImplementation,
		ImplementationName:      servarrImplementation,
		ConfigContract:          servarrConfigContract,
		Protocol:                servarrProtocol,
		EnableRss:               d.Enabled,
		EnableAutomaticSearch:   d.Enabled,
		EnableInteractiveSearch: d.Enabled,
		Priority:                d.Priority,
		Fields:                  fields,
		Tags:                    []int{},
	}
}

// List returns the app's Torznab indexers, recovering the harbrr slug from each
// row's feed URL so reconciliation can recognize its own.
func (s *servarrDriver) List(ctx context.Context) ([]RemoteIndexer, error) {
	var raw []servarrIndexer
	if _, err := s.do(ctx, http.MethodGet, servarrIndexerPath, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]RemoteIndexer, 0, len(raw))
	for _, r := range raw {
		if !strings.EqualFold(r.Implementation, servarrImplementation) {
			continue
		}
		feedURL := fieldString(r.Fields, "baseUrl")
		out = append(out, RemoteIndexer{
			RemoteID: strconv.Itoa(r.ID), Name: r.Name,
			FeedURL: feedURL, ManagedBySlug: slugFromFeedURL(feedURL),
		})
	}
	return out, nil
}

func (s *servarrDriver) Create(ctx context.Context, d DesiredIndexer) (string, error) {
	var resp servarrIndexer
	if _, err := s.do(ctx, http.MethodPost, servarrIndexerPath, s.buildIndexer(d), &resp); err != nil {
		return "", err
	}
	return strconv.Itoa(resp.ID), nil
}

func (s *servarrDriver) Update(ctx context.Context, remoteID string, d DesiredIndexer) error {
	id, err := strconv.Atoi(remoteID)
	if err != nil {
		return fmt.Errorf("appsync: %s: invalid remote id %q: %w", s.kind, remoteID, err)
	}
	body := s.buildIndexer(d)
	body.ID = id
	_, err = s.do(ctx, http.MethodPut, servarrIndexerPath+"/"+remoteID, body, nil)
	return err
}

func (s *servarrDriver) Delete(ctx context.Context, remoteID string) error {
	_, err := s.do(ctx, http.MethodDelete, servarrIndexerPath+"/"+remoteID, nil, nil)
	return err
}

func (s *servarrDriver) Test(ctx context.Context, d DesiredIndexer) error {
	_, err := s.do(ctx, http.MethodPost, servarrIndexerPath+"/test", s.buildIndexer(d), nil)
	return err
}

// do performs one authenticated request, decoding a 2xx body into out (when non-nil)
// and turning any non-2xx into a scrubbed error. The request body (which carries the
// harbrr feed key) is never echoed into an error; the X-Api-Key header is never logged.
func (s *servarrDriver) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("appsync: %s: marshal request: %w", s.kind, err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("appsync: %s: build request: %w", s.kind, scrubURLError(err))
	}
	req.Header.Set("X-Api-Key", s.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("appsync: %s: %s %s: %w", s.kind, method, path, scrubURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, s.statusError(method, path, resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("appsync: %s: decode %s: %w", s.kind, path, err)
		}
	}
	return resp.StatusCode, nil
}

// statusError builds an error from a non-2xx response. The body is deliberately NOT
// echoed: it is attacker/app-shaped free text that can reproduce the request — which
// carries the harbrr feed key — so the status code alone is surfaced (the app's own
// logs hold the detail). This keeps the credential off every error surface.
func (s *servarrDriver) statusError(method, path string, resp *http.Response) error {
	return fmt.Errorf("appsync: %s: %s %s: status %d", s.kind, method, path, resp.StatusCode)
}

// scrubURLError strips the request URL from a *url.Error so a credential a user may
// have embedded in an app's base URL (userinfo) can never reach an error surface
// (last_sync_error, an API response) — RedactError does not scrub URL userinfo. The Op
// and underlying cause are kept (host:port in a dial error is not a secret); any other
// error passes through unchanged. Shared by both drivers' do().
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// field builds a typed field entry; the value marshals cleanly (string/int slice/bool
// never error), so a marshal failure is impossible and ignored.
func field(name string, v any) servarrField {
	b, _ := json.Marshal(v)
	return servarrField{Name: name, Value: b}
}

// fieldString reads a named string field's value, or "" when absent/non-string.
func fieldString(fields []servarrField, name string) string {
	for _, f := range fields {
		if f.Name != name {
			continue
		}
		var v string
		if err := json.Unmarshal(f.Value, &v); err == nil {
			return v
		}
		return ""
	}
	return ""
}

// intsOrEmpty makes a nil slice serialize as [] rather than null.
func intsOrEmpty(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

// animeCats is the anime subset Sonarr wants in animeCategories (Newznab 5070).
func animeCats(cats []int) []int {
	out := []int{}
	for _, c := range cats {
		if c == newznabAnimeCategory {
			out = append(out, c)
		}
	}
	return out
}
