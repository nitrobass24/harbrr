// Package appsync pushes harbrr's configured indexers into the *arr/qui apps that
// consume Torznab (Sonarr, Radarr, autobrr/qui) — the Phase 10 "drop-in Prowlarr"
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
	Priority   int
	Enabled    bool
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
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%v\x00%d\x00%t", d.Name, d.FeedURL, cats, d.Priority, d.Enabled)
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
// URL: {origin}/api/v2.0/indexers/{slug}/results/torznab.
const feedURLMarker = "/api/v2.0/indexers/"

// slugFromFeedURL recovers the harbrr slug embedded in a Torznab feed URL, or "" when
// the URL is not a harbrr feed. Drivers use it to tag which of an app's indexers are
// harbrr-managed (so orphan removal never touches a human-added one).
func slugFromFeedURL(feedURL string) string {
	i := strings.Index(feedURL, feedURLMarker)
	if i < 0 {
		return ""
	}
	rest := feedURL[i+len(feedURLMarker):]
	j := strings.Index(rest, "/")
	if j <= 0 {
		return ""
	}
	return rest[:j]
}

// Target is one app's sync driver: it marshals a DesiredIndexer into the app's REST
// dialect and performs the lifecycle calls. The reconciler drives it; drivers hold no
// reconciliation logic of their own. Create returns the id the app assigned.
type Target interface {
	Kind() string
	List(ctx context.Context) ([]RemoteIndexer, error)
	Create(ctx context.Context, d DesiredIndexer) (remoteID string, err error)
	Update(ctx context.Context, remoteID string, d DesiredIndexer) error
	Delete(ctx context.Context, remoteID string) error
	Test(ctx context.Context, d DesiredIndexer) error
}
