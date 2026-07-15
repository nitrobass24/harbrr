package normalizer

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// fakeCategoryMap builds a real mapper.CategoryMap from (trackerID -> category
// name) pairs by routing through mapper.Build, so the test exercises the same
// MapTrackerCat* lookups the engine uses. Duplicate trackerIDs accumulate into
// a multi-category mapping.
func fakeCategoryMap(t *testing.T, mappings [][2]string) *mapper.CategoryMap {
	t.Helper()
	def := &loader.Definition{ID: "fake"}
	for _, m := range mappings {
		def.Caps.CategoryMappings = append(def.Caps.CategoryMappings, loader.CategoryMapping{
			ID:  loader.Scalar{Value: m[0], Set: true},
			Cat: m[1],
		})
	}
	def.Caps.Modes.Search = []string{"q"}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("building fake category map: %v", err)
	}
	return caps.CategoryMap
}

func TestParseSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"plain GB", "1.5 GB", 1610612736},
		{"no space", "1.5GB", 1610612736},
		{"MB", "700 MB", 734003200},
		{"KB", "512 KB", 524288},
		{"TB", "2 TB", 2199023255552},
		{"KiB equals KB", "512 KiB", 524288},
		{"MiB equals MB", "8 MiB", 8388608},
		{"comma decimal", "3,5GB", 3758096384},
		{"thousands then decimal", "1.018,29 MB", 1067754432},
		{"lowercase unit", "4 gb", 4294967296},
		{"no unit raw bytes", "1048576", 1048576},
		{"dash zero", "-", 0},
		{"triple dash zero", "---", 0},
		{"empty", "", 0},
		{"bytes word", "123 B", 123},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseSize(tt.in); got != tt.want {
				t.Errorf("parseSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestCoerceLong(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int64
	}{
		{"42", 42},
		{"1,234", 1234},
		{"1.234", 1234},
		{"12 seeders", 12},
		{"", 0},
		{"-", 0},
		{"n/a", 0},
		{"  7  ", 7},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := coerceLong(tt.in); got != tt.want {
				t.Errorf("coerceLong(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestCoerceDouble(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want float64
	}{
		{"1.0", 1.0},
		{"0.5", 0.5},
		{"0", 0},
		{"", 0},
		{"1,5", 1.5},
		{"x2.25y", 2.25},
		{"1.234,56", 1234.56},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := coerceDouble(tt.in); got != tt.want {
				t.Errorf("coerceDouble(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFirstIntRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int64
	}{
		{"tt1234567", 1234567},
		{"https://imdb.com/title/tt0111161/", 111161},
		{"12345", 12345},
		{"none", 0},
		{"", 0},
		{"abc99def88", 99},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := firstIntRun(tt.in); got != tt.want {
				t.Errorf("firstIntRun(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// baseRow is a minimal valid field row; tests clone and tweak it.
func baseRow() map[string]string {
	return map[string]string{
		"title":    "Some Release 1080p",
		"size":     "1.5 GB",
		"seeders":  "10",
		"category": "1",
		"download": "/download/123",
	}
}

func TestReleaseFreeleechVolumeFactors(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	n := New(WithCategoryMap(cm))

	tests := []struct {
		name      string
		dvf       string
		setDVF    bool
		wantDVF   float64
		assertRaw string // substring expected in marshaled JSON
	}{
		{"absent defaults to 1.0", "", false, 1.0, `"downloadVolumeFactor":1`},
		{"explicit freeleech 0.0 survives", "0", true, 0.0, `"downloadVolumeFactor":0`},
		{"half leech 0.5", "0.5", true, 0.5, `"downloadVolumeFactor":0.5`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			row := baseRow()
			if tt.setDVF {
				row["downloadvolumefactor"] = tt.dvf
			}
			r, err := n.Release(row)
			if err != nil {
				t.Fatalf("Release: %v", err)
			}
			if r.DownloadVolumeFactor != tt.wantDVF {
				t.Errorf("DownloadVolumeFactor = %v, want %v", r.DownloadVolumeFactor, tt.wantDVF)
			}
			b, err := Marshal([]*Release{r})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.Contains(string(b), tt.assertRaw) {
				t.Errorf("marshaled JSON missing %q\n got: %s", tt.assertRaw, b)
			}
		})
	}
}

// TestZeroVolumeFactorNotOmitted is the explicit freeleech-survival guard: a
// 0.0 download volume factor must appear in the JSON, never be dropped by
// omitempty.
func TestZeroVolumeFactorNotOmitted(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["downloadvolumefactor"] = "0"
	row["uploadvolumefactor"] = "0"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	b, _ := Marshal([]*Release{r})
	for _, want := range []string{`"downloadVolumeFactor":0`, `"uploadVolumeFactor":0`, `"size":1610612736`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("expected %q in JSON, got: %s", want, b)
		}
	}
}

func TestReleaseCategories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mappings [][2]string
		catID    string
		catDesc  string
		want     []int
	}{
		{
			name:     "single",
			mappings: [][2]string{{"1", "Movies/HD"}},
			catID:    "1",
			want:     []int{2040},
		},
		{
			name:     "multi sorted",
			mappings: [][2]string{{"1", "TV/HD"}, {"1", "Movies/HD"}},
			catID:    "1",
			want:     []int{2040, 5040},
		},
		{
			name:     "by description",
			mappings: [][2]string{{"7", "Audio/MP3"}},
			catDesc:  "Audio/MP3",
			// A desc mapping also synthesises Jackett's custom (1:1) category at
			// the +100000 offset, so the desc lookup resolves to both; sorted.
			want: []int{3010, 100007},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The description path needs the desc recorded in the mapping; build
			// mappings carrying the desc when catDesc is exercised.
			defMappings := tt.mappings
			cm := buildCatMapWithDesc(t, defMappings, tt.catDesc)
			row := baseRow()
			delete(row, "category")
			if tt.catID != "" {
				row["category"] = tt.catID
			}
			if tt.catDesc != "" {
				row["categorydesc"] = tt.catDesc
			}
			r, err := New(WithCategoryMap(cm)).Release(row)
			if err != nil {
				t.Fatalf("Release: %v", err)
			}
			if !equalInts(r.Categories, tt.want) {
				t.Errorf("Categories = %v, want %v", r.Categories, tt.want)
			}
			if !sort.IntsAreSorted(r.Categories) {
				t.Errorf("Categories not sorted: %v", r.Categories)
			}
		})
	}
}

// buildCatMapWithDesc builds a CategoryMap, attaching desc to each mapping when
// desc is non-empty so the categorydesc lookup path resolves.
func buildCatMapWithDesc(t *testing.T, mappings [][2]string, desc string) *mapper.CategoryMap {
	t.Helper()
	def := &loader.Definition{ID: "fake"}
	def.Caps.Modes.Search = []string{"q"}
	for _, m := range mappings {
		def.Caps.CategoryMappings = append(def.Caps.CategoryMappings, loader.CategoryMapping{
			ID:   loader.Scalar{Value: m[0], Set: true},
			Cat:  m[1],
			Desc: desc,
		})
	}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("building category map: %v", err)
	}
	return caps.CategoryMap
}

func TestMagnetFromInfoHash(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	delete(row, "download")
	row["infohash"] = "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=") {
		t.Errorf("synthesized magnet wrong: %q", r.Magnet)
	}
	// dn must be the URL-encoded title.
	if !strings.Contains(r.Magnet, "dn=Some+Release+1080p") {
		t.Errorf("magnet dn not url-encoded title: %q", r.Magnet)
	}
	if !strings.Contains(r.Magnet, "&tr=") {
		t.Errorf("magnet missing trackers: %q", r.Magnet)
	}
}

// TestPrivateIndexerDoesNotSynthesizeMagnet mirrors Jackett FixResults: a
// private indexer must NOT generate a public magnet from an info hash, while
// the reverse direction (magnet -> info hash) stays unconditional.
func TestPrivateIndexerDoesNotSynthesizeMagnet(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["infohash"] = "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
	r, err := New(WithType("private"), WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.Magnet != "" {
		t.Errorf("private indexer synthesized a magnet from info hash: %q", r.Magnet)
	}
	if r.InfoHash != "ABCDEF0123456789ABCDEF0123456789ABCDEF01" {
		t.Errorf("info hash should be preserved: %q", r.InfoHash)
	}
}

// TestPrivateIndexerStillExtractsInfoHashFromMagnet confirms the magnet ->
// info hash direction is not gated by indexer type.
func TestPrivateIndexerStillExtractsInfoHashFromMagnet(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	delete(row, "download")
	row["magnet"] = "magnet:?xt=urn:btih:DEADBEEF0123456789ABCDEF0123456789ABCDEF&dn=Title"
	r, err := New(WithType("private"), WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.InfoHash != "DEADBEEF0123456789ABCDEF0123456789ABCDEF" {
		t.Errorf("InfoHash from magnet = %q", r.InfoHash)
	}
}

func TestInfoHashFromMagnet(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	delete(row, "download")
	row["magnet"] = "magnet:?xt=urn:btih:DEADBEEF0123456789ABCDEF0123456789ABCDEF&dn=Title"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.InfoHash != "DEADBEEF0123456789ABCDEF0123456789ABCDEF" {
		t.Errorf("InfoHash from magnet = %q", r.InfoHash)
	}
}

func TestDownloadMagnetGoesToMagnetField(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["download"] = "magnet:?xt=urn:btih:AABBCCDDEEFF00112233445566778899AABBCCDD&dn=x"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.Link != "" {
		t.Errorf("magnet download should not set Link, got %q", r.Link)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:") {
		t.Errorf("magnet download should set Magnet, got %q", r.Magnet)
	}
	if r.InfoHash != "AABBCCDDEEFF00112233445566778899AABBCCDD" {
		t.Errorf("InfoHash = %q", r.InfoHash)
	}
}

func TestRelativeURLResolution(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["download"] = "/torrents/download/42"
	row["details"] = "details/42"
	row["poster"] = "/img/42.jpg"
	r, err := New(WithBaseURL("https://tracker.example/"), WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	cases := map[string]string{
		"Link":    "https://tracker.example/torrents/download/42",
		"Details": "https://tracker.example/details/42",
		"Poster":  "https://tracker.example/img/42.jpg",
	}
	got := map[string]string{"Link": r.Link, "Details": r.Details, "Poster": r.Poster}
	for field, want := range cases {
		if got[field] != want {
			t.Errorf("%s = %q, want %q", field, got[field], want)
		}
	}
}

func TestAbsoluteURLUnchanged(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["download"] = "https://cdn.example/get/9"
	r, err := New(WithBaseURL("https://tracker.example/"), WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.Link != "https://cdn.example/get/9" {
		t.Errorf("absolute Link altered: %q", r.Link)
	}
}

func TestPeersEqualsSeedersPlusLeechers(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["seeders"] = "10"
	row["leechers"] = "3"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.Seeders != 10 || r.Leechers != 3 || r.Peers != 13 {
		t.Errorf("seeders/leechers/peers = %d/%d/%d, want 10/3/13", r.Seeders, r.Leechers, r.Peers)
	}
}

func TestSeedersSanityCap(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["seeders"] = "9999999"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.Seeders != 0 {
		t.Errorf("absurd seeders not clamped: %d", r.Seeders)
	}
}

func TestRequiredTrioErrors(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	n := New(WithCategoryMap(cm))

	tests := []struct {
		name string
		drop string
		want error
	}{
		{"missing title", "title", errNoTitle},
		{"missing size", "size", errNoSize},
		{"missing seeders", "seeders", errNoSeeders},
		{"missing category", "category", errNoCategory},
		{"missing link", "download", errNoLink},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			row := baseRow()
			delete(row, tt.drop)
			_, err := n.Release(row)
			if err == nil {
				t.Fatalf("expected error dropping %q", tt.drop)
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestErrorsNeverLeakSecrets confirms required-field errors reference field
// names only, never values that could carry a passkey.
func TestErrorsNeverLeakSecrets(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["title"] = ""
	row["download"] = "https://x.example/dl?passkey=SECRETVALUE"
	_, err := New(WithCategoryMap(cm)).Release(row)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRETVALUE") {
		t.Errorf("error leaked secret: %v", err)
	}
}

func TestMarshalDeterministic(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "TV/HD"}, {"1", "Movies/HD"}})
	n := New(WithBaseURL("https://t.example/"), WithCategoryMap(cm))
	build := func() []*Release {
		titles := []string{"A 1080p", "B 720p"}
		rs := make([]*Release, 0, len(titles))
		for _, title := range titles {
			row := baseRow()
			row["title"] = title
			row["leechers"] = "2"
			r, err := n.Release(row)
			if err != nil {
				t.Fatalf("Release: %v", err)
			}
			rs = append(rs, r)
		}
		return rs
	}
	a, err := Marshal(build())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b, err := Marshal(build())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("marshal not deterministic:\n a=%s\n b=%s", a, b)
	}
	// Categories must be sorted ascending in the output.
	if !strings.Contains(string(a), `"categories":[2040,5040]`) {
		t.Errorf("categories not sorted ascending in JSON: %s", a)
	}
}

func TestMarshalNilIsEmptyArray(t *testing.T) {
	t.Parallel()
	b, err := Marshal(nil)
	if err != nil {
		t.Fatalf("Marshal(nil): %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("Marshal(nil) = %q, want []", b)
	}
}

func TestNormalizeGenre(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"Action/Sci_Fi", "Action,Sci Fi"},
		{"Drama, Comedy", "Drama,Comedy"},
		{"", ""},
		{"Horror|Horror", "Horror"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := normalizeGenre(tt.in); got != tt.want {
				t.Errorf("normalizeGenre(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIMDBFormat(t *testing.T) {
	t.Parallel()
	cm := fakeCategoryMap(t, [][2]string{{"1", "Movies/HD"}})
	row := baseRow()
	row["imdbid"] = "tt0111161"
	r, err := New(WithCategoryMap(cm)).Release(row)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if r.IMDBID != "tt0111161" {
		t.Errorf("IMDBID = %q, want tt0111161", r.IMDBID)
	}
}

// TestCorpusFieldCensus is the headline parity check: every STANDARD base field
// name used anywhere in the vendored corpus must be handled by the Release
// model, or a corpus field would be silently dropped. Intermediate Result-
// context keys (leading "_") are ignored, and the "<field>_<suffix>" form is
// reduced to its base field name.
func TestCorpusFieldCensus(t *testing.T) {
	t.Parallel()
	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	t.Logf("corpus: %d definitions loaded, %d skipped", len(defs), len(skipped))

	// handled is the set of STANDARD Cardigann base field names the Release
	// model reads. It is the parity contract for this census: every
	// non-intermediate field name a definition uses must appear here, or a
	// corpus field would be silently dropped. Keep it in sync with Release.
	handled := map[string]struct{}{
		"title": {}, "description": {}, "details": {}, "comments": {},
		"download": {}, "magnet": {}, "infohash": {},
		"size": {}, "category": {}, "categorydesc": {},
		"seeders": {}, "leechers": {}, "files": {}, "grabs": {},
		"date": {}, "downloadvolumefactor": {}, "uploadvolumefactor": {},
		"minimumratio": {}, "minimumseedtime": {},
		"imdb": {}, "imdbid": {}, "tmdbid": {}, "tvdbid": {}, "tvmazeid": {},
		"traktid": {}, "doubanid": {}, "rageid": {},
		"genre": {}, "year": {}, "poster": {},
		"author": {}, "booktitle": {}, "publisher": {},
		"album": {}, "artist": {}, "label": {}, "track": {},
	}
	counts := map[string]int{}
	var unmodeled []string
	hasDownloadOrMagnetOrHash := false
	hasCategorySource := false

	for _, d := range defs {
		for _, entry := range d.Search.Fields.Ordered() {
			base := standardFieldBase(entry.Key)
			if base == "" {
				continue // intermediate "_"-prefixed key
			}
			if _, ok := handled[base]; !ok {
				unmodeled = append(unmodeled, base)
				continue
			}
			counts[base]++
			switch base {
			case "download", "magnet", "infohash":
				hasDownloadOrMagnetOrHash = true
			case "category", "categorydesc":
				hasCategorySource = true
			}
		}
	}

	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		t.Logf("field %-22s used in %d definitions", k, counts[k])
	}

	if len(unmodeled) > 0 {
		sort.Strings(unmodeled)
		t.Fatalf("corpus uses field names not handled by the Release model (silent-drop risk): %v",
			dedupe(unmodeled))
	}
	if !hasDownloadOrMagnetOrHash {
		t.Error("corpus census found no download/magnet/infohash field — acquisition link not modeled")
	}
	if !hasCategorySource {
		t.Error("corpus census found no category/categorydesc field — category source not modeled")
	}
	for _, req := range []string{"title", "size", "seeders"} {
		if counts[req] == 0 {
			t.Errorf("required field %q not observed in corpus census", req)
		}
	}
}

// standardFieldBase reduces a definition field key to its STANDARD base field
// name: drop the "|modifier" suffix, then drop a trailing "_<suffix>". Keys
// whose base begins with "_" are intermediate Result-context variables and
// return "".
func standardFieldBase(key string) string {
	base := strings.SplitN(key, "|", 2)[0]
	if strings.HasPrefix(base, "_") {
		return ""
	}
	if i := strings.IndexByte(base, '_'); i >= 0 {
		base = base[:i]
	}
	return base
}

func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
