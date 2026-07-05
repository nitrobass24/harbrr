// Package appsync pushes harbrr's configured indexers into the *arr/qui apps that
// consume Torznab (Sonarr, Radarr, autobrr/qui) — the "drop-in Prowlarr"
// feature. A target-neutral DesiredIndexer is reconciled against each app's current
// state by a small pure engine (reconcile.go); per-app REST dialects live behind the
// Target interface (one driver per app). Secrets are redacted in logs and never
// logged in pushed bodies.
package appsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Category is a Newznab category harbrr advertises for an indexer: the numeric id
// every app keys on plus its human name (which qui stores per-indexer). It is
// self-contained so drivers never reach into the engine's category table.
type Category struct {
	ID   int
	Name string
}

// DesiredIndexer is harbrr's target-neutral intent for one indexer on one
// connection: what every driver pushes before its app-specific marshalling. Slug is
// the reconciliation key (it also appears in FeedURL, enabling recovery when the
// persisted remote id is missing). APIKey is the harbrr key the app presents back on
// the feed; it is a secret — never log a DesiredIndexer verbatim.
type DesiredIndexer struct {
	Slug       string
	Name       string
	FeedURL    string
	APIKey     string
	Categories []Category
	// Capabilities is the flat Torznab capability token list (e.g. "tv-search",
	// "tv-search-imdbid", "movie-search") for targets that store caps per indexer
	// rather than fetching them from the feed. Only qui consumes it; Servarr pulls
	// caps from the feed itself, so its driver ignores this.
	Capabilities []string
	Priority     int
	Enabled      bool
	// EnableRss / EnableAutomaticSearch / EnableInteractiveSearch are the per-search-mode
	// flags a Servarr indexer is registered under. Each is resolved in buildDesired as the
	// instance's Enabled AND the sync profile's matching toggle (no profile → all three
	// equal Enabled), so a disabled instance forces every flag false regardless of profile.
	// qui ignores them — it has a single Enabled flag.
	EnableRss               bool
	EnableAutomaticSearch   bool
	EnableInteractiveSearch bool
	// MinSeeders is the Torznab minimum-seeders floor pushed into a Servarr indexer, taken
	// from the sync profile (0 = unset → the app default, not pushed). Torznab-only: it is
	// never sent on a Newznab/usenet indexer.
	MinSeeders int
	// Protocol is the remote download protocol the app should register this indexer
	// under: "torrent" (Torznab, the default for an empty value) or "usenet" (Newznab).
	// It selects the Servarr Implementation/ConfigContract/protocol; qui has no usenet
	// notion and skips usenet indexers entirely (see buildDesired).
	Protocol string
}

// CategoryIDs returns just the numeric ids (Servarr's categories field).
func (d DesiredIndexer) CategoryIDs() []int {
	ids := make([]int, 0, len(d.Categories))
	for _, c := range d.Categories {
		ids = append(ids, c.ID)
	}
	return ids
}

// hash is a stable fingerprint of the pushed intent; an unchanged hash lets reconcile
// skip the remote update. It deliberately excludes APIKey: the per-connection feed key
// is immutable (minted once at create, never rotated in place — a new key means a new
// connection), so it can't change between syncs and keeps the secret out of this fast,
// non-password hash. Category names come from the static Newznab table, so the ids
// alone fingerprint categories.
func (d DesiredIndexer) hash() string {
	cats := d.CategoryIDs()
	sort.Ints(cats)
	caps := append([]string(nil), d.Capabilities...)
	sort.Strings(caps)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%v\x00%v\x00%d\x00%t", d.Name, d.FeedURL, cats, caps, d.Priority, d.Enabled)
	// Protocol joins the fingerprint only when it diverges from the torrent default,
	// so a pre-usenet torrent indexer keeps its original PayloadHash (no spurious
	// re-sync on upgrade); a usenet indexer fingerprints distinctly.
	if d.Protocol != "" && d.Protocol != "torrent" {
		fmt.Fprintf(h, "\x00%s", d.Protocol)
	}
	// MinSeeders joins only when set (>0) — same divergence rule as Protocol: a
	// profile-less connection (MinSeeders 0) keeps its exact prior PayloadHash, so no
	// fleet-wide re-push happens on upgrade; a profile that sets a floor fingerprints
	// distinctly.
	if d.MinSeeders > 0 {
		fmt.Fprintf(h, "\x00ms=%d", d.MinSeeders)
	}
	// The enable triple joins only when a toggle diverges from Enabled (a sync profile
	// narrowed the search modes). With no profile all three equal Enabled — already
	// captured by the %t above — so the fingerprint is unchanged and no spurious re-sync
	// happens on upgrade; a profile that flips a toggle fingerprints distinctly.
	if d.EnableRss != d.Enabled || d.EnableAutomaticSearch != d.Enabled || d.EnableInteractiveSearch != d.Enabled {
		fmt.Fprintf(h, "\x00en=%t,%t,%t", d.EnableRss, d.EnableAutomaticSearch, d.EnableInteractiveSearch)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RemoteIndexer is one indexer as it currently exists in a target app, reduced to
// what reconciliation needs. ManagedBySlug is non-empty only when the driver
// recognizes the row as harbrr-managed (it recovered a harbrr slug from the row's
// feed URL) — orphan removal touches only those rows, never human-added indexers.
type RemoteIndexer struct {
	RemoteID      string
	Name          string
	FeedURL       string
	ManagedBySlug string
}

// feedURLMarker is the fixed path segment that precedes a harbrr slug in every feed
// URL: {origin}/api/indexers/{slug}/results/torznab. It is shared with the management
// API (/api/indexers/{slug}/...), so slugFromFeedURL additionally requires the feed
// suffix below to avoid reading a management URL as a feed.
const feedURLMarker = "/api/indexers/"

// feedURLSuffix is the path segment that follows the slug in a feed URL (the four
// results/torznab variants all start with it). Requiring it is what distinguishes a
// feed URL from a management URL that shares the /api/indexers/{slug} prefix.
const feedURLSuffix = "/results/torznab"

// slugFromFeedURL recovers the harbrr slug embedded in a Torznab feed URL, or "" when
// the URL is not a harbrr feed. Drivers use it to tag which of an app's indexers are
// harbrr-managed (so orphan removal never touches a human-added one). The marker is
// matched against the URL *path* only — a query/fragment occurrence of the marker must
// not be read as ownership (which could orphan-delete a human-added indexer).
func slugFromFeedURL(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil {
		return ""
	}
	i := strings.Index(u.Path, feedURLMarker)
	if i < 0 {
		return ""
	}
	rest := u.Path[i+len(feedURLMarker):]
	j := strings.Index(rest, "/")
	if j <= 0 {
		return ""
	}
	// Require the feed suffix so a management URL sharing the /api/indexers/{slug}
	// prefix (e.g. .../search) is not misread as a harbrr-managed feed.
	if !strings.HasPrefix(rest[j:], feedURLSuffix) {
		return ""
	}
	return rest[:j]
}

// Target is one app's sync driver: it marshals a DesiredIndexer into the app's REST
// dialect and performs the lifecycle calls. The reconciler drives it; drivers hold no
// reconciliation logic of their own. Create returns the id the app assigned. Kept to
// five methods (the repo's interface-size rule); the per-app kind is carried by the
// connection, not the driver.
type Target interface {
	List(ctx context.Context) ([]RemoteIndexer, error)
	Create(ctx context.Context, d DesiredIndexer) (remoteID string, err error)
	Update(ctx context.Context, remoteID string, d DesiredIndexer) error
	Delete(ctx context.Context, remoteID string) error
	Test(ctx context.Context, d DesiredIndexer) error
}
