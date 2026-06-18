package appsync

import (
	"context"
	"fmt"

	"github.com/autobrr/harbrr/internal/domain"
)

// Reconciliation action labels (the Action of an IndexerOutcome).
const (
	ActionCreated = "created"
	ActionUpdated = "updated"
	ActionNoop    = "noop"
	ActionDeleted = "deleted"
	ActionFailed  = "failed"
)

// LedgerEntry is the persisted prior state for one harbrr slug on a connection: the
// remote id we last assigned and the hash of the last intent we pushed. The service
// loads it from app_connection_indexers and hands it to Reconcile keyed by slug.
type LedgerEntry struct {
	RemoteID    string
	PayloadHash string
}

// IndexerOutcome is what reconciliation did for one slug. The service persists it back
// into the ledger (create/update/noop → upsert remote id + hash; deleted → drop the
// row; failed → record the scrubbed error and keep the row for the next attempt).
type IndexerOutcome struct {
	Slug     string
	Action   string
	RemoteID string
	Hash     string
	Err      error
}

// Reconcile drives a target into agreement with the desired set. It add-or-updates
// every desired indexer and, only at sync level "full", removes harbrr-owned orphans
// the desired set no longer contains. Each indexer is its own unit: a single
// Create/Update/Delete failure is captured as an ActionFailed outcome and never aborts
// the others (partial-failure isolation). A failed List is fatal (nothing can be
// reconciled) and is the only error returned.
func Reconcile(ctx context.Context, t Target, syncLevel string, desired []DesiredIndexer, prior map[string]LedgerEntry) ([]IndexerOutcome, error) {
	remote, err := t.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("appsync: list %s indexers: %w", t.Kind(), err)
	}
	ownedBySlug, existingIDs := indexRemote(remote)

	outcomes := make([]IndexerOutcome, 0, len(desired))
	desiredSlugs := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		desiredSlugs[d.Slug] = struct{}{}
		outcomes = append(outcomes, addOrUpdate(ctx, t, d, prior[d.Slug], ownedBySlug[d.Slug], existingIDs))
	}
	if syncLevel == domain.SyncLevelFull {
		outcomes = append(outcomes, removeOrphans(ctx, t, remote, desiredSlugs)...)
	}
	return outcomes, nil
}

// addOrUpdate creates the indexer when no remote id resolves, no-ops when the prior
// hash still matches, and otherwise updates it.
func addOrUpdate(ctx context.Context, t Target, d DesiredIndexer, prior, owned ledgerOrRemote, existingIDs map[string]struct{}) IndexerOutcome {
	hash := d.hash()
	remoteID := resolveRemoteID(prior, owned, existingIDs)
	if remoteID == "" {
		rid, err := t.Create(ctx, d)
		if err != nil {
			return IndexerOutcome{Slug: d.Slug, Action: ActionFailed, Err: err}
		}
		return IndexerOutcome{Slug: d.Slug, Action: ActionCreated, RemoteID: rid, Hash: hash}
	}
	if prior.id() == remoteID && prior.hash() == hash {
		return IndexerOutcome{Slug: d.Slug, Action: ActionNoop, RemoteID: remoteID, Hash: hash}
	}
	if err := t.Update(ctx, remoteID, d); err != nil {
		return IndexerOutcome{Slug: d.Slug, Action: ActionFailed, RemoteID: remoteID, Err: err}
	}
	return IndexerOutcome{Slug: d.Slug, Action: ActionUpdated, RemoteID: remoteID, Hash: hash}
}

// resolveRemoteID picks the id to update: the persisted one when it still exists
// remotely (authoritative), else the id recovered from a feed-URL slug match
// (recovery after lost state), else "" to signal a create.
func resolveRemoteID(prior, owned ledgerOrRemote, existingIDs map[string]struct{}) string {
	if id := prior.id(); id != "" {
		if _, ok := existingIDs[id]; ok {
			return id
		}
	}
	return owned.id() // recovered remote id, or "" when no managed row matches the slug
}

// removeOrphans deletes harbrr-owned remote rows whose slug is no longer desired. Rows
// harbrr does not own (ManagedBySlug == "") are never touched.
func removeOrphans(ctx context.Context, t Target, remote []RemoteIndexer, desiredSlugs map[string]struct{}) []IndexerOutcome {
	var out []IndexerOutcome
	for _, r := range remote {
		if r.ManagedBySlug == "" {
			continue
		}
		if _, ok := desiredSlugs[r.ManagedBySlug]; ok {
			continue
		}
		if err := t.Delete(ctx, r.RemoteID); err != nil {
			out = append(out, IndexerOutcome{Slug: r.ManagedBySlug, Action: ActionFailed, RemoteID: r.RemoteID, Err: err})
			continue
		}
		out = append(out, IndexerOutcome{Slug: r.ManagedBySlug, Action: ActionDeleted, RemoteID: r.RemoteID})
	}
	return out
}

// indexRemote builds the lookup structures reconcile needs: harbrr-owned rows keyed by
// slug, and the set of all existing remote ids (to validate a persisted id).
func indexRemote(remote []RemoteIndexer) (map[string]RemoteIndexer, map[string]struct{}) {
	owned := make(map[string]RemoteIndexer)
	ids := make(map[string]struct{}, len(remote))
	for _, r := range remote {
		ids[r.RemoteID] = struct{}{}
		if r.ManagedBySlug != "" {
			owned[r.ManagedBySlug] = r
		}
	}
	return owned, ids
}

// Status collapses per-indexer outcomes into a connection-level status: ok when none
// failed, error when all failed, partial otherwise. No outcomes (nothing to sync) is ok.
func Status(outcomes []IndexerOutcome) string {
	var ok, failed int
	for _, o := range outcomes {
		if o.Action == ActionFailed {
			failed++
			continue
		}
		ok++
	}
	switch {
	case failed == 0:
		return domain.SyncStatusOK
	case ok == 0:
		return domain.SyncStatusError
	default:
		return domain.SyncStatusPartial
	}
}

// ledgerOrRemote is the small "has an id (and maybe a hash)" view addOrUpdate needs,
// satisfied by both a persisted LedgerEntry and a recovered RemoteIndexer, so the
// resolve logic reads the same for either source.
type ledgerOrRemote interface {
	id() string
	hash() string
}

func (l LedgerEntry) id() string   { return l.RemoteID }
func (l LedgerEntry) hash() string { return l.PayloadHash }

func (r RemoteIndexer) id() string   { return r.RemoteID }
func (r RemoteIndexer) hash() string { return "" }
