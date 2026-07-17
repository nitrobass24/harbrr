package nebulance

import (
	"errors"
	stdhttp "net/http"
	"os"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func TestParseReleases(t *testing.T) {
	t.Parallel()
	driver := buildDriver(t)
	releases, err := driver.parseReleases(readFixture(t, "search_response.json"))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("releases = %d, want 3", len(releases))
	}

	season := releases[0]
	if season.Title != "Example.Show.S02.2160p.WEB-DL-GRP.mkv" {
		t.Errorf("title = %q", season.Title)
	}
	if len(season.Categories) != 1 || season.Categories[0] != 5045 {
		t.Errorf("categories = %v, want TV/UHD", season.Categories)
	}
	if season.Size != 8589934592 || season.Files != 12 || season.Seeders != 20 || season.Leechers != 4 || season.Peers != 24 || season.Grabs != 99 {
		t.Errorf("season numeric mapping mismatch: %+v", season)
	}
	if season.PublishDate != "2026-07-13T01:02:03Z" {
		t.Errorf("publish date = %q", season.PublishDate)
	}
	if season.MinimumSeedTime != 432000 || season.DownloadVolumeFactor != 0 || season.UploadVolumeFactor != 1 {
		t.Error("season ratio/seed-time mapping mismatch")
	}
	if season.IMDBID != "tt1234567" || season.TVMazeID != 42 {
		t.Errorf("external IDs = %q/%d", season.IMDBID, season.TVMazeID)
	}
	if season.Details != defaultBaseURL+"torrents.php?id=101" || season.GUID != season.Details {
		t.Error("details/GUID mapping mismatch")
	}

	episode := releases[1]
	if episode.Title != "Fallback Group Name" {
		t.Errorf("fallback title = %q", episode.Title)
	}
	if len(episode.Categories) != 1 || episode.Categories[0] != 5000 {
		t.Errorf("episode categories = %v, want generic TV for an empty release title", episode.Categories)
	}
	if episode.MinimumSeedTime != 86400 {
		t.Errorf("episode minimum seed time = %d, want 86400", episode.MinimumSeedTime)
	}

	sd := releases[2]
	if len(sd.Categories) != 1 || sd.Categories[0] != 5030 {
		t.Errorf("SD categories = %v, want TV/SD", sd.Categories)
	}
}

func TestQualityCategoryParity(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Example.S01.2160p.WEB-DL-GRP":  "4",
		"Example.S01.1080p.HDTV-GRP":    "3",
		"Example.S01.HDTV.XviD-GRP":     "2",
		"Example.S01.576p.BluRay-GRP":   "2",
		"Example.S01.BluRay.XviD-GRP":   "2",
		"Example.S01.HR.WS.PDTV-GRP":    "3",
		"Example.S01.2160p.Unknown-GRP": "1",
	}
	for title, want := range tests {
		if got := qualityCategory(title); got != want {
			t.Errorf("qualityCategory(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		return response(stdhttp.StatusOK, `{"total_results":0,"items":[]}`), nil
	}})
	if _, err := driver.parseReleases([]byte("<html>")); !errors.Is(err, search.ErrParseError) {
		t.Errorf("malformed err = %v, want ErrParseError", err)
	}
	for _, tt := range []struct {
		name      string
		body      string
		wantLogin bool
	}{
		{name: "bare auth response", body: "\nInvalid params\r\n", wantLogin: true},
		{name: "HTML help response", body: "<html><body>Help: Invalid params. Check the API documentation.</body></html>"},
		{name: "plain help response", body: "Help: Invalid params means the request syntax is unsupported."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := driver.parseReleases([]byte(tt.body))
			if tt.wantLogin {
				if !errors.Is(err, login.ErrLoginFailed) {
					t.Errorf("err = %v, want ErrLoginFailed", err)
				}
				return
			}
			if !errors.Is(err, search.ErrParseError) || errors.Is(err, login.ErrLoginFailed) {
				t.Errorf("err = %v, want only ErrParseError", err)
			}
		})
	}
	if _, err := driver.parseReleases([]byte(`{"error":{"message":"Invalid key ` + testAPIKey + `"}}`)); err == nil || !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("error envelope err = %v, want ErrLoginFailed", err)
	} else if strings.Contains(err.Error(), testAPIKey) {
		t.Error("error envelope leaked API key")
	}
	if _, err := driver.parseReleases([]byte(`{"error":{"message":"Invalid page"}}`)); !errors.Is(err, search.ErrParseError) || errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("invalid page err = %v, want only ErrParseError", err)
	}
	if _, err := driver.parseReleases([]byte(`{"total_results":1,"items":[{"rls_utc":"bad"}]}`)); !errors.Is(err, search.ErrParseError) {
		t.Errorf("bad date err = %v, want ErrParseError", err)
	}
}

func TestParseResponseEnvelope(t *testing.T) {
	t.Parallel()
	driver := buildDriver(t)
	tests := []struct {
		name      string
		body      string
		wantErr   error
		wantCount int
	}{
		{name: "valid empty", body: `{"total_results":0,"items":[]}`},
		{name: "maintenance object", body: `{"maintenance":true}`, wantErr: search.ErrParseError},
		{name: "unknown object", body: `{"message":"temporarily unavailable"}`, wantErr: search.ErrParseError},
		{
			name:      "auth marker in release title",
			body:      `{"total_results":1,"items":[{"rls_name":"Show.Invalid API Key.S01E01","rls_utc":"2026-07-13T01:02:03Z"}]}`,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			releases, err := driver.parseReleases([]byte(tt.body))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(releases) != tt.wantCount {
				t.Errorf("releases = %d, want %d", len(releases), tt.wantCount)
			}
		})
	}
}
