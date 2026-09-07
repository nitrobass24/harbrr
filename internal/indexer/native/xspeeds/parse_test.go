package xspeeds

import (
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

func TestParseReleases(t *testing.T) {
	driver := newTestDriver(t, "https://www.xspeeds.eu/", testConfig(), nil, nil)
	releases, err := driver.parseReleases(readFixture(t, "browse_mixed.html"))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 4 {
		t.Fatalf("release count = %d, want 4", len(releases))
	}

	free := releases[0]
	if free.Title != "Free Anime" || free.Details != "https://www.xspeeds.eu/details.php?id=1" || free.GUID != free.Details {
		t.Errorf("free identity = %+v", free)
	}
	if free.Link != "https://www.xspeeds.eu/download.php?id=1" {
		t.Errorf("free Link = %q", free.Link)
	}
	if free.Description != "Scene,WEBDL" || free.Size != 1_610_612_736 {
		t.Errorf("free description/size = %q/%d", free.Description, free.Size)
	}
	if free.Seeders != 10 || free.Leechers != 4 || free.Peers != 14 || free.Grabs != 1234 {
		t.Errorf("free stats = seeders %d leechers %d peers %d grabs %d", free.Seeders, free.Leechers, free.Peers, free.Grabs)
	}
	if free.PublishDate != "2026-08-16T14:30:00Z" || free.DownloadVolumeFactor != 0 || free.UploadVolumeFactor != 1 || free.MinimumRatio != 0.8 {
		t.Errorf("free date/factors = %+v", free)
	}
	if !slices.Contains(free.Categories, 5070) {
		t.Errorf("free categories = %v, want TV/Anime", free.Categories)
	}

	sitewide := releases[1]
	if sitewide.Link != "https://www.xspeeds.eu/download.php?id=2" || sitewide.DownloadVolumeFactor != 0 || sitewide.UploadVolumeFactor != 2 {
		t.Errorf("sitewide = %+v", sitewide)
	}
	if !slices.Contains(sitewide.Categories, 2000) || !slices.Contains(sitewide.Categories, 5000) {
		t.Errorf("category 139 = %v, want Movies and TV", sitewide.Categories)
	}

	silver := releases[2]
	if silver.DownloadVolumeFactor != 0.5 || silver.Size != 0 || silver.Grabs != 0 || silver.Seeders != 0 || silver.Leechers != 0 || silver.PublishDate != "" {
		t.Errorf("silver degradation = %+v", silver)
	}
	if !slices.Contains(silver.Categories, 2000) || !slices.Contains(silver.Categories, 5000) {
		t.Errorf("category 138 = %v, want Movies and TV", silver.Categories)
	}

	uncategorized := releases[3]
	if uncategorized.DownloadVolumeFactor != 1 || len(uncategorized.Categories) != 0 {
		t.Errorf("uncategorized normal release = %+v", uncategorized)
	}
}

func TestResolveSameOriginURL(t *testing.T) {
	base, err := url.Parse("https://xspeeds.example/root/")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "relative", raw: "download.php?id=1", want: "https://xspeeds.example/root/download.php?id=1"},
		{name: "absolute same origin", raw: "https://xspeeds.example/root/download.php?id=1", want: "https://xspeeds.example/root/download.php?id=1"},
		{name: "cross host", raw: "https://evil.example/download.php?id=1", wantErr: true},
		{name: "scheme relative cross host", raw: "//evil.example/download.php?id=1", wantErr: true},
		{name: "userinfo", raw: "https://user@xspeeds.example/root/download.php?id=1", wantErr: true},
		{name: "scheme downgrade", raw: "http://xspeeds.example/root/download.php?id=1", wantErr: true},
		{name: "non HTTP", raw: "file://xspeeds.example/root/download.php?id=1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveSameOriginURL(base, test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("resolveSameOriginURL() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("resolveSameOriginURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseFreeleechOnly(t *testing.T) {
	cfg := testConfig()
	cfg["freeleech_only"] = "true"
	driver := newTestDriver(t, "https://www.xspeeds.eu/", cfg, nil, nil)
	releases, err := driver.parseReleases(readFixture(t, "browse_mixed.html"))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 2 || releases[0].Title != "Free Anime" || releases[1].Title != "Sitewide Double" {
		t.Errorf("freeleech releases = %v", releaseTitles(releases))
	}
}

func TestParseEmpty(t *testing.T) {
	driver := newTestDriver(t, "https://www.xspeeds.eu/", testConfig(), nil, nil)
	releases, err := driver.parseReleases(readFixture(t, "browse_empty.html"))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 0 {
		t.Errorf("release count = %d, want 0", len(releases))
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func releaseTitles(releases []*normalizer.Release) []string {
	titles := make([]string, len(releases))
	for i, release := range releases {
		titles[i] = release.Title
	}
	return titles
}
