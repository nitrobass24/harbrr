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
)

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
	Categories []int
	Priority   int
	Enabled    bool
}

// hash is a stable fingerprint of the pushed intent. An unchanged hash lets reconcile
// skip the remote update; a rotated APIKey changes it (so the new key is re-pushed).
func (d DesiredIndexer) hash() string {
	cats := append([]int(nil), d.Categories...)
	sort.Ints(cats)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%v\x00%d\x00%t", d.Name, d.FeedURL, d.APIKey, cats, d.Priority, d.Enabled)
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
