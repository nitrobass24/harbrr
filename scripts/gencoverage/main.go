// Command gencoverage regenerates website/docs/coverage.md: the full
// built-vs-live-tested status table for every tracker harbrr knows about —
// the embedded Cardigann corpus plus the native (non-vendored) drivers, built
// and planned.
//
// Run from the repo root:
//
//	go run ./scripts/gencoverage > website/docs/coverage.md
//
// The corpus rows come straight from the loader's embedded snapshot, so the
// list stays in sync with the vendored defs. The native lists and the
// live-tested set are curated below and updated by hand as drivers ship and
// trackers are validated.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// liveTested is the set of def / driver ids confirmed against the real tracker
// (Prowlarr differential + a real grab). Keyed by loader id for the corpus and
// by driver id for natives.
var liveTested = map[string]bool{
	// Cardigann corpus, live-validated in an operator instance. The "-api"
	// suffixed ids are the UNIT3D API-variant defs those indexers use.
	"aither-api": true, "anthelion-api": true,
	"aura4k-api": true, "digitalcore-api": true, "hdspace": true, "lst": true,
	"luminarr-api": true, "onlyencodes-api": true, "racing4everyone-api": true, "reelflix-api": true,
	"retromoviesclub-api": true, "torrentleech": true, "yuscene-api": true, "racingforme": true,
	"seedpool-api": true, "uploadcx": true, "darkpeers-api": true,
	// Native drivers, live-validated.
	"iptorrents": true, "filelist": true, "myanonamouse": true, "broadcastthenet": true,
	"passthepopcorn": true, "hdbits": true,
	// BrokenStones, live-validated 2026-07-17: full parity vs the Prowlarr oracle
	// (696 = 696 across harbrr's Torznab pages, head titles identical in order).
	"brokenstones": true,
	// Usenet, live-validated against the deployed fix build (differential 100=100 vs
	// Prowlarr): the generic Newznab driver via the dognzb preset (search + a real .nzb
	// grab through /dl), and the NZBIndex native driver (search).
	"newznab": true, "nzbindex": true,
	// Torznab family, live-validated 2026-07-14 (differential 14=14, Jaccard 1.00, vs
	// the operator's Prowlarr Torznab-preset oracle, with SMOKE_GRAB link resolution):
	// the MoreThanTV preset. The sibling presets and the generic entry are offline-only.
	"morethantv": true,
}

type nativeRow struct {
	name    string
	pattern string
	id      string // for the live-tested lookup; "" if n/a
	issue   int    // planned drivers: GitHub issue number; 0 if built
}

// nativeBuilt are the shipped native (non-Cardigann) drivers.
var nativeBuilt = []nativeRow{
	{name: "AvistaZ", pattern: "Bearer (login → token)", id: "avistaz"},
	{name: "CinemaZ", pattern: "Bearer (login → token)", id: "cinemaz"},
	{name: "PrivateHD", pattern: "Bearer (login → token)", id: "privatehd"},
	{name: "ExoticaZ", pattern: "Bearer (login → token)", id: "exoticaz"},
	{name: "IPTorrents", pattern: "Session cookie (HTML scrape)", id: "iptorrents"},
	{name: "TorrentDay", pattern: "Session cookie (HTML scrape)", id: "torrentday"},
	{name: "FileList", pattern: "Passkey / JSON API", id: "filelist"},
	{name: "HDBits", pattern: "Passkey / JSON API", id: "hdbits"},
	{name: "BeyondHD", pattern: "Passkey / JSON API", id: "beyondhd"},
	{name: "MyAnonamouse", pattern: "Session cookie (rotating, JSON)", id: "myanonamouse"},
	{name: "Redacted", pattern: "Gazelle (cookie/key → ajax.php)", id: "redacted"},
	{name: "Orpheus", pattern: "Gazelle (cookie/key → ajax.php)", id: "orpheus"},
	{name: "AlphaRatio", pattern: "Gazelle (session cookie → ajax.php)", id: "alpharatio"},
	{name: "BrokenStones", pattern: "Gazelle (session cookie → ajax.php)", id: "brokenstones"},
	{name: "BroadcastTheNet", pattern: "Bespoke API", id: "broadcastthenet"},
	{name: "PassThePopcorn", pattern: "Bespoke API", id: "passthepopcorn"},
	{name: "GazelleGames", pattern: "Bespoke API", id: "gazellegames"},
	{name: "AnimeBytes", pattern: "Bespoke API", id: "animebytes"},
	{name: "Nebulance", pattern: "Bespoke JSON API", id: "nebulance"},
	{name: "Usenet (Newznab)", pattern: "Generic Newznab", id: "newznab"},
	{name: "NZBIndex", pattern: "Bespoke JSON API (public)", id: "nzbindex"},
	{name: "MoreThanTV", pattern: "Torznab API (native)", id: "morethantv"},
	{name: "AnimeTosho", pattern: "Torznab API (native)", id: "animetosho"},
	{name: "Torrent Network", pattern: "Torznab API (native)", id: "torrentnetwork"},
	{name: "Torznab (generic)", pattern: "Generic Torznab", id: "torznab"},
}

// nativePlanned are native drivers we have issues for but haven't built.
var nativePlanned = []nativeRow{
	{name: "SpeedCD", pattern: "Session cookie (HTML scrape)", issue: 21},
	{name: "FunFile", pattern: "Session cookie (HTML scrape)", issue: 23},
	{name: "BitHDTV", pattern: "Session cookie (HTML scrape)", issue: 24},
	{name: "TorrentBytes", pattern: "Session cookie (HTML scrape)", issue: 33},
	{name: "XSpeeds", pattern: "Session cookie (HTML scrape)", issue: 34},
	{name: "PreToMe", pattern: "Session cookie (HTML scrape)", issue: 35},
	{name: "RevolutionTT", pattern: "Session cookie (HTML scrape)", issue: 36},
	{name: "MTeam", pattern: "Passkey / JSON API", issue: 25},
	{name: "NorBits", pattern: "Passkey / JSON API", issue: 26},
	{name: "SceneHD", pattern: "Passkey / JSON API", issue: 27},
	{name: "DICMusic", pattern: "Gazelle (username / password)", issue: 28},
	{name: "Libble", pattern: "Gazelle (username / password)", issue: 29},
	{name: "GreatPosterWall", pattern: "Gazelle (username / password)", issue: 30},
	{name: "RuTracker", pattern: "Public / niche", issue: 37},
	{name: "LostFilm", pattern: "Public / niche", issue: 38},
	{name: "Toloka", pattern: "Public / niche", issue: 39},
	{name: "SubsPlease", pattern: "Public / niche", issue: 40},
	{name: "AudioBookBay", pattern: "Public / niche", issue: 41},
}

func check(b bool) string {
	if b {
		return "✅"
	}
	return "⬜"
}

func esc(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func main() {
	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}

	byType := map[string][]*loader.Definition{}
	for _, d := range defs {
		byType[d.Type] = append(byType[d.Type], d)
	}
	for _, group := range byType {
		sort.Slice(group, func(i, j int) bool {
			return strings.ToLower(group[i].Name) < strings.ToLower(group[j].Name)
		})
	}

	var b strings.Builder
	writeIntro(&b, len(defs), len(nativeBuilt), len(nativePlanned), len(skipped))
	writeNative(&b, "Native drivers", nativeBuilt)
	writePlanned(&b)
	writeCorpus(&b, byType)
	writeFooter(&b)

	fmt.Print(b.String())
	fmt.Fprintf(os.Stderr, "corpus=%d skipped=%d nativeBuilt=%d nativePlanned=%d\n",
		len(defs), len(skipped), len(nativeBuilt), len(nativePlanned))
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "  skipped: %s (%s)\n", s.ID, s.Reason)
	}
	// Surface any live-tested id that didn't match a loaded def/driver.
	known := map[string]bool{}
	for _, d := range defs {
		known[d.ID] = true
	}
	for _, n := range append(nativeBuilt, nativePlanned...) {
		if n.id != "" {
			known[n.id] = true
		}
	}
	for id := range liveTested {
		if !known[id] {
			fmt.Fprintf(os.Stderr, "  WARN live-tested id not found: %s\n", id)
		}
	}
}

func writeIntro(b *strings.Builder, corpus, built, planned, skipped int) {
	total := corpus + built + planned
	fmt.Fprintf(b, `# Tracker coverage & status

Every tracker harbrr knows about — the embedded Cardigann corpus plus the native drivers
Cardigann can't express — and how far each is validated.

- **Built** ✅ — harbrr serves it (a Cardigann definition ships, or a native Go driver does) and
  it passes its offline golden tests. ⬜ means a driver is planned but not built yet.
- **Live-tested** ✅ — confirmed against the real tracker: a Prowlarr differential plus a real
  grab. ⬜ means built and offline-validated, but not yet live-verified (usually just needs an
  account on that tracker). See **[Test status](test-status.md)** for the evidence behind this
  column and the auth/fetch patterns proven live.

**%d trackers** total: %d Cardigann corpus (all built) · %d native drivers built · %d native
drivers planned. To configure one, see **[Adding an indexer](guides/add-indexer.md)**.
`, total, corpus, built, planned)
	if skipped > 0 {
		fmt.Fprintf(b, "\n> %d corpus definition(s) currently fail to load — listed at the bottom.\n", skipped)
	}
	b.WriteString("\n")
}

func writeNative(b *strings.Builder, heading string, rows []nativeRow) {
	fmt.Fprintf(b, "## %s\n\nBespoke code in Jackett/Prowlarr (no Cardigann definition); harbrr ships native Go drivers.\n\n", heading)
	b.WriteString("| Tracker | Pattern | Built | Live-tested |\n|---|---|:--:|:--:|\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %s | ✅ | %s |\n", esc(r.name), esc(r.pattern), check(liveTested[r.id]))
	}
	b.WriteString("\n")
}

func writePlanned(b *strings.Builder) {
	b.WriteString("### Planned — vote for yours\n\nNative drivers we have issues for but haven't built. 👍 or comment on the issue for the one you want and it moves up the queue.\n\n")
	b.WriteString("| Tracker | Pattern | Built | Live-tested |\n|---|---|:--:|:--:|\n")
	for _, r := range nativePlanned {
		link := fmt.Sprintf("[%s](https://github.com/autobrr/harbrr/issues/%d)", esc(r.name), r.issue)
		fmt.Fprintf(b, "| %s | %s | ⬜ | ⬜ |\n", link, esc(r.pattern))
	}
	b.WriteString("\n")
}

var typeOrder = []struct{ key, label string }{
	{"private", "Private"},
	{"semi-private", "Semi-private"},
	{"public", "Public"},
}

func writeCorpus(b *strings.Builder, byType map[string][]*loader.Definition) {
	b.WriteString("## Cardigann corpus\n\nServed through the shared engine from the vendored Jackett snapshot — all built. Live-tested where an operator instance covers them.\n\n")
	for _, t := range typeOrder {
		group := byType[t.key]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(b, "### %s (%d)\n\n| Tracker | Built | Live-tested |\n|---|:--:|:--:|\n", t.label, len(group))
		for _, d := range group {
			fmt.Fprintf(b, "| %s | ✅ | %s |\n", esc(d.Name), check(liveTested[d.ID]))
		}
		b.WriteString("\n")
	}
}

func writeFooter(b *strings.Builder) {
	b.WriteString(`## Don't see yours?

[Open an issue](https://github.com/autobrr/harbrr/issues/new) and describe the tracker — if it's
a Cardigann definition it may already work; if it needs a native driver, it joins the planned list
above.

---

*This page is generated by ` + "`scripts/gencoverage`" + ` from the embedded definitions. To refresh
it: ` + "`go run ./scripts/gencoverage > website/docs/coverage.md`" + `.*
`)
}
