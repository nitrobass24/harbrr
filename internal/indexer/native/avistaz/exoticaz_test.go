package avistaz

import (
	"os"
	"reflect"
	"testing"
)

// TestExoticaZCategories proves the ExoticaZ parser variant derives categories from the
// response `category` dict (mapped through the site's XXX caps) rather than from
// type+video_quality. Like Prowlarr's ExoticaZParser it uses MapTrackerCatToNewznab,
// which (per Jackett) yields the standard newznab id AND the synthesized custom "1:1"
// category (tracker id + 100000): key "1" -> 6040 + 100001, {"2","5"} -> 6050/6010 +
// 100002/100005, "6" -> 6040 + 100006 (deduped + sorted). The base movie/tv/music parser
// emits the standard id only (it uses the NewznabStandardCategory, not the tracker map).
func TestExoticaZCategories(t *testing.T) {
	t.Parallel()
	d, ok := buildDriver(t, "exoticaz", "").(*driver)
	if !ok {
		t.Fatal("exoticaz family did not build the native driver")
	}
	body, err := os.ReadFile("testdata/exoticaz_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	want := []struct {
		title string
		cats  []int
	}{
		{"Clip One 1080p", []int{6040, 100001}},
		{"Pack Two", []int{6010, 6050, 100002, 100005}},
		{"BluRay Three", []int{6040, 100006}},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d releases, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Title != w.title {
			t.Errorf("release[%d] title = %q, want %q", i, got[i].Title, w.title)
		}
		if !reflect.DeepEqual(got[i].Categories, w.cats) {
			t.Errorf("release[%d] categories = %v, want %v", i, got[i].Categories, w.cats)
		}
	}
}
