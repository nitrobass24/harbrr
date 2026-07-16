package torznab

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"time"
	"unicode/utf8"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// fuzzReleases is a quick.Generator that builds adversarial release slices:
// arbitrary lengths, interspersed nil pointers, and field values that stress the
// serializer (control bytes, invalid UTF-8, lone surrogates, BOM/noncharacters,
// XML metacharacters, astral runes, megabyte-ish strings, and extreme numbers).
type fuzzReleases []*normalizer.Release

func (fuzzReleases) Generate(rng *rand.Rand, size int) reflect.Value {
	n := rng.Intn(size%8 + 1) // 0..7 releases per case keeps the property fast
	rs := make([]*normalizer.Release, n)
	for i := range rs {
		if rng.Intn(8) == 0 {
			continue // ~1/8 nil — the serializer must skip these, never panic
		}
		rs[i] = randomRelease(rng)
	}
	return reflect.ValueOf(fuzzReleases(rs))
}

func randomRelease(rng *rand.Rand) *normalizer.Release {
	return &normalizer.Release{
		Title:                nastyString(rng),
		Description:          nastyString(rng),
		Details:              nastyString(rng),
		Comments:             nastyString(rng),
		Link:                 nastyString(rng),
		Magnet:               nastyString(rng),
		InfoHash:             nastyString(rng),
		Poster:               nastyString(rng),
		Genre:                nastyString(rng),
		Author:               nastyString(rng),
		BookTitle:            nastyString(rng),
		Publisher:            nastyString(rng),
		Album:                nastyString(rng),
		Artist:               nastyString(rng),
		Label:                nastyString(rng),
		Track:                nastyString(rng),
		IMDBID:               nastyString(rng),
		PublishDate:          nastyString(rng),
		Size:                 nastyInt(rng),
		Seeders:              nastyInt(rng),
		Leechers:             nastyInt(rng),
		Peers:                nastyInt(rng),
		Grabs:                nastyInt(rng),
		Files:                nastyInt(rng),
		Year:                 nastyInt(rng),
		TMDBID:               nastyInt(rng),
		TVDBID:               nastyInt(rng),
		MinimumSeedTime:      nastyInt(rng),
		Categories:           randomCats(rng),
		DownloadVolumeFactor: nastyFloat(rng),
		UploadVolumeFactor:   nastyFloat(rng),
		MinimumRatio:         nastyFloat(rng),
	}
}

func nastyString(rng *rand.Rand) string {
	switch rng.Intn(9) {
	case 0:
		return ""
	case 1:
		return "Ubuntu 24.04 LTS (2024)"
	case 2: // C0/C1 control bytes
		b := make([]byte, rng.Intn(24))
		for i := range b {
			b[i] = byte(rng.Intn(0x20))
		}
		return string(b)
	case 3: // arbitrary (likely invalid) UTF-8 bytes
		b := make([]byte, rng.Intn(32))
		_, _ = rng.Read(b)
		return string(b)
	case 4: // XML metacharacters and a CDATA-close sequence
		return `a<b>c&d"e'f ]]> <!-- --> &amp;`
	case 5: // BOM (U+FEFF), noncharacters, a lone-surrogate byte run, and a real U+FFFD
		return "\uFEFF\uFFFE\uFFFF" + string([]byte{0xED, 0xA0, 0x80}) + "\uFFFD"
	case 6: // large value
		return strings.Repeat("Z", 4000+rng.Intn(8000))
	case 7: // random runes including the astral planes (emoji etc.)
		var sb strings.Builder
		for i := 0; i < rng.Intn(40); i++ {
			sb.WriteRune(rune(rng.Intn(0x110000)))
		}
		return sb.String()
	default: // tab/newline/CR (valid XML whitespace, must be preserved)
		return "line1\nline2\tcol\r\n"
	}
}

func nastyInt(rng *rand.Rand) int64 {
	switch rng.Intn(6) {
	case 0:
		return 0
	case 1:
		return -1
	case 2:
		return math.MinInt64
	case 3:
		return math.MaxInt64
	case 4:
		return rng.Int63()
	default:
		return -rng.Int63()
	}
}

func nastyFloat(rng *rand.Rand) float64 {
	switch rng.Intn(7) {
	case 0:
		return 0
	case 1:
		return 1
	case 2:
		return math.NaN()
	case 3:
		return math.Inf(1)
	case 4:
		return math.Inf(-1)
	case 5:
		return math.Copysign(0, -1)
	default:
		return rng.NormFloat64() * 1e9
	}
}

func randomCats(rng *rand.Rand) []int {
	n := rng.Intn(5)
	if n == 0 {
		return nil
	}
	c := make([]int, n)
	for i := range c {
		c[i] = rng.Intn(200000) - 2000 // includes negative, standard, and custom-range ids
	}
	return c
}

// TestMarshalResultsRobustness asserts that MarshalResults, over arbitrary
// release shapes, never panics and always emits well-formed,
// valid-UTF-8, namespace-bound XML. A fixed seed keeps it deterministic for
// -race -count=1.
func TestMarshalResultsRobustness(t *testing.T) {
	feed := FeedInfo{
		IndexerID: "fuzz", Name: "Fuzz Tracker", Description: "fuzz",
		SiteLink: "https://fuzz.test/", Type: "private", SelfURL: "https://fuzz.test/api",
	}
	now := time.Date(2026, time.June, 14, 0, 0, 0, 0, time.UTC)

	prop := func(fr fuzzReleases) bool {
		data, err := marshalResults(feed, fr, now)
		if err != nil {
			t.Logf("MarshalResults returned error on %d releases: %v", len(fr), err)
			return false
		}
		if !utf8.Valid(data) {
			t.Logf("output is not valid UTF-8")
			return false
		}
		// Full parse: any ill-formedness (bad nesting, illegal char in an
		// attribute, unescaped metacharacter) surfaces as a decode error.
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, terr := dec.Token()
			if errors.Is(terr, io.EOF) {
				break
			}
			if terr != nil {
				t.Logf("output did not re-parse as well-formed XML: %v", terr)
				return false
			}
		}
		if !bytes.Contains(data, []byte(`xmlns:torznab=`)) {
			t.Logf("output is missing the torznab namespace binding")
			return false
		}
		return true
	}

	cfg := &quick.Config{MaxCount: 400, Rand: rand.New(rand.NewSource(20260614))} //nolint:gosec // deterministic test seed.
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("MarshalResults robustness property failed: %v", err)
	}
}
