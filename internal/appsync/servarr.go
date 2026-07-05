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

	apphttp "github.com/autobrr/harbrr/internal/http"
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
	servarrNewznabImpl    = "Newznab"
	servarrNewznabConfig  = "NewznabSettings"
	servarrUsenetProtocol = "usenet"
	// Servarr forks serve the indexer REST API under different API versions: Sonarr,
	// Radarr, and Whisparr are v3; Lidarr and Readarr are v1. The version affects only
	// the request URL path (the marshalled body is identical), so it is carried as a
	// per-driver field rather than baked into buildIndexer.
	servarrIndexerPathV3 = "/api/v3/indexer"
	servarrIndexerPathV1 = "/api/v1/indexer"
	newznabAnimeCategory = 5070
	// forceSaveQuery makes Servarr persist the indexer even when its add/update-time
	// validation test doesn't pass (e.g. "no results in the configured categories" for
	// a marginal tracker) — Prowlarr parity, so a transient/warning test result doesn't
	// hard-fail the sync. harbrr already category-prefilters before pushing (buildDesired),
	// and health is tracked separately; a genuine failure now surfaces via statusError.
	forceSaveQuery = "?forceSave=true"
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

// servarrDriver implements Target for a Servarr-shaped app (Sonarr/Radarr/Lidarr/
// Readarr/Whisparr). indexerPath carries the app's indexer REST path (v1 or v3).
type servarrDriver struct {
	kind        string
	baseURL     string
	apiKey      string
	client      *http.Client
	anime       bool
	indexerPath string
}

var _ Target = (*servarrDriver)(nil)

// newServarr builds a Servarr driver. apiKey is the *app's* key (to authenticate to
// it); the harbrr feed key travels inside each pushed indexer body. indexerPath is the
// app's indexer REST path (servarrIndexerPathV3 or servarrIndexerPathV1).
func newServarr(kind, baseURL, apiKey string, client *http.Client, anime bool, indexerPath string) *servarrDriver {
	if client == nil {
		client = defaultHTTPClient()
	}
	return &servarrDriver{
		kind: kind, baseURL: strings.TrimRight(baseURL, "/"),
		apiKey: apiKey, client: client, anime: anime, indexerPath: indexerPath,
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
	// Usenet indexers register as Newznab; everything else (including an empty Protocol)
	// stays Torznab. The fields[] set above is legal for both — NewznabSettings carries
	// the same baseUrl/apiPath/apiKey/categories (+ Sonarr animeCategories) names.
	impl, cfg, proto := servarrImplementation, servarrConfigContract, servarrProtocol
	if d.Protocol == servarrUsenetProtocol {
		impl, cfg, proto = servarrNewznabImpl, servarrNewznabConfig, servarrUsenetProtocol
	}
	// minimumSeeders is a TorznabSettings-only field (NewznabSettings has no seeders
	// notion), so it rides only the torrent branch and only when the profile set it (>0).
	// Omitted → the app's own default, exactly as before sync profiles existed.
	if proto == servarrProtocol && d.MinSeeders > 0 {
		fields = append(fields, field("minimumSeeders", d.MinSeeders))
	}
	return servarrIndexer{
		Name:                    d.Name,
		Implementation:          impl,
		ImplementationName:      impl,
		ConfigContract:          cfg,
		Protocol:                proto,
		EnableRss:               d.EnableRss,
		EnableAutomaticSearch:   d.EnableAutomaticSearch,
		EnableInteractiveSearch: d.EnableInteractiveSearch,
		Priority:                d.Priority,
		Fields:                  fields,
		Tags:                    []int{},
	}
}

// List returns the app's Torznab indexers, recovering the harbrr slug from each
// row's feed URL so reconciliation can recognize its own.
func (s *servarrDriver) List(ctx context.Context) ([]RemoteIndexer, error) {
	var raw []servarrIndexer
	if _, err := s.do(ctx, http.MethodGet, s.indexerPath, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]RemoteIndexer, 0, len(raw))
	for _, r := range raw {
		// Both implementations are harbrr-managed: Torznab (torrent) and Newznab
		// (usenet). Missing either here would make harbrr-managed usenet rows invisible
		// to reconcile and silently orphan them.
		if !strings.EqualFold(r.Implementation, servarrImplementation) &&
			!strings.EqualFold(r.Implementation, servarrNewznabImpl) {
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
	if _, err := s.do(ctx, http.MethodPost, s.indexerPath+forceSaveQuery, s.buildIndexer(d), &resp); err != nil {
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
	_, err = s.do(ctx, http.MethodPut, s.indexerPath+"/"+remoteID+forceSaveQuery, body, nil)
	return err
}

func (s *servarrDriver) Delete(ctx context.Context, remoteID string) error {
	_, err := s.do(ctx, http.MethodDelete, s.indexerPath+"/"+remoteID, nil, nil)
	return err
}

func (s *servarrDriver) Test(ctx context.Context, d DesiredIndexer) error {
	_, err := s.do(ctx, http.MethodPost, s.indexerPath+"/test", s.buildIndexer(d), nil)
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

// statusError builds an error from a non-2xx response. Servarr's body carries the
// actual validation detail (why the add/update was rejected), but it also echoes the
// submitted config — which carries the harbrr feed key — so the extracted reason is
// scrubbed at the source (parseServarrReason → RedactError) before it reaches any
// error surface. When the body isn't a shape we recognize, the status alone is kept.
func (s *servarrDriver) statusError(method, path string, resp *http.Response) error {
	if reason := parseServarrReason(resp); reason != "" {
		return fmt.Errorf("appsync: %s: %s %s: status %d: %s", s.kind, method, path, resp.StatusCode, reason)
	}
	return fmt.Errorf("appsync: %s: %s %s: status %d", s.kind, method, path, resp.StatusCode)
}

// servarrValidationFailure is one entry of Servarr's add/update validation-error
// response (a JSON array). Only the human-readable text is read — never the echoed
// submitted config.
type servarrValidationFailure struct {
	PropertyName string `json:"propertyName"`
	ErrorMessage string `json:"errorMessage"`
	Severity     string `json:"severity"`
}

// parseServarrReason extracts a redaction-safe reason from a non-2xx Servarr body: the
// validation-failure array ([{propertyName,errorMessage,severity}]) or a bare {message}
// object. The text is scrubbed (RedactError catches the echoed apiKey/feed-key in its
// JSON `"key":"value"` and `key=value` forms) so the credential never rides the error.
// Returns "" when the body is empty or an unrecognized shape.
func parseServarrReason(resp *http.Response) string {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || len(raw) == 0 {
		return ""
	}
	var failures []servarrValidationFailure
	if err := json.Unmarshal(raw, &failures); err == nil && len(failures) > 0 {
		parts := make([]string, 0, len(failures))
		for _, f := range failures {
			msg := strings.TrimSpace(f.ErrorMessage)
			if msg == "" {
				continue
			}
			if f.PropertyName != "" {
				msg = f.PropertyName + ": " + msg
			}
			parts = append(parts, msg)
		}
		if len(parts) > 0 {
			return scrubReason(strings.Join(parts, "; "))
		}
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && strings.TrimSpace(obj.Message) != "" {
		return scrubReason(obj.Message)
	}
	return ""
}

// scrubReason runs a Servarr-supplied reason through the shared credential redaction
// (RedactError scrubs Authorization/Cookie headers, JSON secret keys, and key=value
// secret tokens — the forms the echoed harbrr feed key takes).
func scrubReason(text string) string {
	return apphttp.RedactError(errors.New(text))
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
