package xspeeds

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	testUsername  = "synthetic-xspeeds-user"
	testPassword  = "synthetic-xspeeds-password"
	testOldCookie = "session=synthetic-xspeeds-old-cookie"
)

func TestDefinition(t *testing.T) {
	families := Families()
	if len(families) != 1 {
		t.Fatalf("Families length = %d, want 1", len(families))
	}
	definition := families[0].Definition
	if definition.ID != "xspeeds" || definition.Links[0] != "https://www.xspeeds.eu/" {
		t.Errorf("definition identity = %q %q", definition.ID, definition.Links[0])
	}
	if definition.RequestDelay == nil || *definition.RequestDelay != 2.1 {
		t.Errorf("RequestDelay = %v, want 2.1", definition.RequestDelay)
	}

	settings := make(map[string]loader.SettingsField, len(definition.Settings))
	for _, field := range definition.Settings {
		settings[field.Name] = field
	}
	if !settings["username"].Required || settings["username"].IsSecret() {
		t.Errorf("username = %+v, want required non-secret text", settings["username"])
	}
	if !settings["password"].Required || !settings["password"].IsSecret() {
		t.Errorf("password = %+v, want required secret", settings["password"])
	}
	if settings["freeleech_only"].Type != "checkbox" {
		t.Errorf("freeleech_only = %+v, want checkbox", settings["freeleech_only"])
	}
	if _, exposed := settings["cookie"]; exposed {
		t.Error("hidden persisted cookie must not be declared as a user-facing setting")
	}

	wantModes := map[string][]string{
		"search":       {"q"},
		"tv-search":    {"q", "season", "ep"},
		"movie-search": {"q"},
		"music-search": {"q"},
		"book-search":  {"q"},
	}
	caps := mustCapabilities(t, definition)
	for mode, want := range wantModes {
		if got := caps.Modes[mode]; !slices.Equal(got, want) {
			t.Errorf("mode %s = %v, want %v", mode, got, want)
		}
	}
	if len(caps.Modes) != len(wantModes) {
		t.Errorf("modes = %v, want only %v", caps.Modes, wantModes)
	}

	gotMappings := make([]string, 0, len(definition.Caps.CategoryMappings))
	for _, mapping := range definition.Caps.CategoryMappings {
		gotMappings = append(gotMappings, fmt.Sprintf("%s|%s|%s", mapping.ID.String(), mapping.Cat, mapping.Desc))
	}
	wantMappings := strings.Split(strings.TrimSpace(expectedMappings), "\n")
	if !slices.Equal(gotMappings, wantMappings) {
		t.Fatalf("category mappings differ\ngot:\n%s\nwant:\n%s", strings.Join(gotMappings, "\n"), expectedMappings)
	}
	for _, id := range []string{"138", "139"} {
		mapped := caps.CategoryMap.MapTrackerCatToNewznab(id)
		if !slices.Contains(mapped, 2000) || !slices.Contains(mapped, 5000) {
			t.Errorf("category %s = %v, want Movies and TV", id, mapped)
		}
	}
}

func TestNewRequiresCookieJar(t *testing.T) {
	_, err := New(native.Params{
		Def:  Families()[0].Definition,
		Cfg:  testConfig(),
		Doer: roundTripDoer(func(*stdhttp.Request) (*stdhttp.Response, error) { return nil, errors.New("unexpected request") }),
	})
	if err == nil || !strings.Contains(err.Error(), "non-nil cookie jar") {
		t.Fatalf("New error = %v, want cookie-jar requirement", err)
	}
}

func TestDriverFlags(t *testing.T) {
	driver := newTestDriver(t, "https://xspeeds.example/", testConfig(), nil, nil)
	if driver.NeedsResolver() {
		t.Error("NeedsResolver = true, want false")
	}
	if !driver.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = false, want true")
	}
}

func newTestServerDriver(t *testing.T, handler stdhttp.Handler, cfg map[string]string, persist func(context.Context, string, string) error) (*driver, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newTestDriver(t, server.URL+"/", cfg, server.Client().Transport, persist), server
}

func newTestDriver(t *testing.T, baseURL string, cfg map[string]string, transport stdhttp.RoundTripper, persist func(context.Context, string, string) error) *driver {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	if transport == nil {
		transport = roundTripDoer(func(*stdhttp.Request) (*stdhttp.Response, error) {
			return &stdhttp.Response{StatusCode: stdhttp.StatusOK, Header: stdhttp.Header{}, Body: stdhttp.NoBody}, nil
		})
	}
	client := &stdhttp.Client{Jar: jar, Transport: transport, CheckRedirect: apphttp.RedirectPolicy}
	built, err := New(native.Params{
		Def:            Families()[0].Definition,
		Cfg:            cfg,
		Doer:           client,
		BaseURL:        baseURL,
		PersistSetting: persist,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.(*driver)
}

func testConfig() map[string]string {
	return map[string]string{"username": testUsername, "password": testPassword}
}

func setTestCookie(writer stdhttp.ResponseWriter, name, value string) {
	//nolint:gosec // G124: synthetic httptest cookies must remain non-Secure so the HTTP test server receives them.
	stdhttp.SetCookie(writer, &stdhttp.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: stdhttp.SameSiteStrictMode})
}

func mustCapabilities(t *testing.T, definition *loader.Definition) *mapper.Capabilities {
	t.Helper()
	caps, err := mapper.Build(definition)
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

type roundTripDoer func(*stdhttp.Request) (*stdhttp.Response, error)

func (do roundTripDoer) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	return do(request)
}

func (do roundTripDoer) Do(request *stdhttp.Request) (*stdhttp.Response, error) {
	return do(request)
}

const expectedMappings = `70|TV/Anime|Anime
113|TV/Anime|Anime Boxsets
112|Movies/Other|Anime Movies
111|Movies/Other|Anime TV
150|PC|Apps
153|Books|Books
154|Audio/Audiobook|Books Audiobooks
155|Books|Books eBooks & Magazines
68|Movies/Other|Cams/TS
140|TV/Documentary|Documentary
10|Movies/DVD|DVDR
109|Movies/BluRay|DVDR Bluray Disc
131|TV/Sport|Fighting
134|TV/Sport|Fighting Boxing
133|TV/Sport|Fighting MMA
132|TV/Sport|Fighting Wrestling
72|Movies/Foreign|Foreign
116|TV/Foreign|Foreign Boxsets
114|Movies/Foreign|Foreign Movies
115|TV/Foreign|Foreign TV
103|Console/Other|Games Console
105|Console/Other|Games Console Nintendo
104|Console/PS4|Games Console Playstation
106|Console/XBox|Games Console XBOX
6|PC/Games|Games PC
108|PC|Games PC Linux
107|PC/Mac|Games PC Mac
11|Movies|Movie Boxsets
118|Movies/UHD|Movie Boxsets 4K
162|Movies/HD|Movie Boxsets AV1
143|Movies/HD|Movie Boxsets HD
119|Movies/HD|Movie Boxsets HEVC
144|Movies/SD|Movie Boxsets SD
12|Movies|Movies
117|Movies/UHD|Movies 4K
163|Movies/HD|Movies AV1
145|Movies/HD|Movies HD
100|Movies/HD|Movies HEVC
146|Movies/SD|Movies SD
13|Audio|Music
135|Audio/Lossless|Music FLAC
151|Audio|Music Karaoke
136|Audio|Music Boxset
148|Audio/Video|Music Videos
9|Other|Other
125|Other|Other Pictures
54|TV/Other|Other Soaps
83|TV/Other|Other Specials
139|TV|TOTM (Freeleech)
138|TV|TOTW (x2 upload)
139|Movies|TOTM (Freeleech)
138|Movies|TOTW (x2 upload)
20|TV/Sport|Sports
88|TV/Sport|Sports/Football
86|TV/Sport|Sports/MotorSports
89|TV/Sport|Sports/Olympics
126|TV|TV
127|TV/UHD|TV 4K
164|TV/HD|TV AV1
129|TV/HD|TV HD
130|TV/HD|TV HEVC
128|TV/SD|TV SD
149|TV|TV Specials
21|TV/SD|TV Boxsets
120|TV/UHD|TV Boxset 4K
165|TV/UHD|TV Boxset AV1
76|TV/HD|TV Boxset HD
97|TV/HD|TV Boxset HEVC
147|TV/SD|TV Boxset SD`
