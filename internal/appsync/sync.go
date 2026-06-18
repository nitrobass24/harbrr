package appsync

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// SyncResult is one indexer's outcome in a sync report (the error is scrubbed).
type SyncResult struct {
	Slug   string
	Action string
	Error  string
}

// SyncReport is the result of a Sync run: the connection-level status plus per-indexer
// outcomes.
type SyncReport struct {
	Status  string
	Results []SyncResult
}

// Sync reconciles harbrr's indexers into one connection's app. A disabled connection
// is skipped (no remote calls). The per-indexer ledger and the connection's last-sync
// status are persisted; a fatal error (cannot list the app) is recorded and returned.
func (s *Service) Sync(ctx context.Context, id int64) (SyncReport, error) {
	conn, err := s.repo.GetConnection(ctx, s.db, id)
	if err != nil {
		return SyncReport{}, fmt.Errorf("appsync: get connection: %w", err)
	}
	if !conn.Enabled {
		return SyncReport{Status: StatusSkipped}, nil
	}
	// The minted harbrr key was revoked out of band (FK SET NULL): pushing the stale
	// key would silently hand the app a dead feed credential, so refuse and record it
	// rather than re-pushing a key harbrr no longer recognizes.
	if conn.HarbrrAPIKeyID == 0 {
		detail := "harbrr api key revoked; recreate the connection to re-mint it"
		s.recordResult(ctx, conn.ID, domain.SyncStatusError, detail)
		return SyncReport{}, fmt.Errorf("%w: %s", ErrInvalid, detail)
	}
	driver, harbrrKey, err := s.driver(conn)
	if err != nil {
		return SyncReport{}, err
	}
	instances, err := s.source.List(ctx)
	if err != nil {
		return SyncReport{}, fmt.Errorf("appsync: list indexers: %w", err)
	}
	ledger, err := s.repo.ListConnectionIndexers(ctx, s.db, conn.ID)
	if err != nil {
		return SyncReport{}, fmt.Errorf("appsync: list connection indexers: %w", err)
	}

	desired, err := s.buildDesired(ctx, instances, conn, harbrrKey, selectedByID(ledger))
	if err != nil {
		return SyncReport{}, err
	}
	outcomes, err := Reconcile(ctx, driver, conn.SyncLevel, desired, priorBySlug(ledger, slugByID(instances)))
	if err != nil {
		s.recordResult(ctx, conn.ID, domain.SyncStatusError, apphttp.RedactError(err))
		return SyncReport{}, err
	}
	if err := s.persistOutcomes(ctx, conn.ID, outcomes, idBySlug(instances)); err != nil {
		// The remote app was already mutated; record the failure so the connection's
		// status reflects reality rather than the prior run's.
		s.recordResult(ctx, conn.ID, domain.SyncStatusError, apphttp.RedactError(err))
		return SyncReport{}, err
	}
	status := Status(outcomes)
	s.recordResult(ctx, conn.ID, status, summaryError(outcomes))
	return SyncReport{Status: status, Results: toResults(outcomes)}, nil
}

// buildDesired projects every in-scope indexer into a DesiredIndexer: the per-app feed
// URL, the connection's harbrr key, and the indexer's categories. Scope "selected"
// keeps only indexers flagged in the ledger.
func (s *Service) buildDesired(ctx context.Context, instances []domain.IndexerInstance, conn domain.AppConnection, harbrrKey string, selected map[int64]bool) ([]DesiredIndexer, error) {
	out := make([]DesiredIndexer, 0, len(instances))
	for _, inst := range instances {
		if conn.IndexScope == domain.IndexScopeSelected && !selected[inst.ID] {
			continue
		}
		cats, err := s.source.Categories(ctx, inst.Slug)
		if err != nil {
			return nil, fmt.Errorf("appsync: categories for %q: %w", inst.Slug, err)
		}
		out = append(out, DesiredIndexer{
			Slug: inst.Slug, Name: inst.Name, FeedURL: feedURL(conn.HarbrrURL, inst.Slug),
			APIKey: harbrrKey, Categories: cats, Priority: conn.Priority, Enabled: inst.Enabled,
		})
	}
	return out, nil
}

// persistOutcomes writes the outcomes back to the ledger in one transaction so a
// mid-loop failure never leaves the ledger half-written: a deleted orphan drops its
// row; everything else upserts the remote id, payload hash, and scrubbed status. The
// selected flag is user intent (owned by SetSelectedIndexers) — reconcile never
// authors it, so a re-sync can't silently re-select a deselected indexer.
func (s *Service) persistOutcomes(ctx context.Context, connID int64, outcomes []IndexerOutcome, idBySlug map[string]int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("appsync: begin ledger tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.clock()
	for _, o := range outcomes {
		instID, ok := idBySlug[o.Slug]
		if !ok {
			continue // an orphan whose harbrr instance is already gone — nothing to record
		}
		if err := s.persistOne(ctx, tx, connID, instID, o, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("appsync: commit ledger: %w", err)
	}
	return nil
}

// persistOne writes a single outcome's ledger effect within the transaction.
func (s *Service) persistOne(ctx context.Context, tx dbinterface.Execer, connID, instID int64, o IndexerOutcome, now time.Time) error {
	if o.Action == ActionDeleted {
		if err := s.repo.DeleteConnectionIndexer(ctx, tx, connID, instID); err != nil {
			return fmt.Errorf("appsync: delete ledger row: %w", err)
		}
		return nil
	}
	row := domain.AppConnectionIndexer{
		ConnectionID: connID, InstanceID: instID, RemoteID: o.RemoteID, Selected: true,
		PayloadHash: o.Hash, LastPushedAt: &now,
		LastPushStatus: pushStatus(o.Action), LastPushError: apphttp.RedactError(o.Err),
	}
	if err := s.repo.UpsertConnectionIndexer(ctx, tx, row); err != nil {
		return fmt.Errorf("appsync: upsert ledger row: %w", err)
	}
	return nil
}

// recordResult persists the connection-level sync outcome (best-effort: a failure to
// record is logged, not propagated over the sync result itself).
func (s *Service) recordResult(ctx context.Context, connID int64, status, detail string) {
	if err := s.repo.RecordSyncResult(ctx, s.db, connID, status, detail, s.clock()); err != nil {
		s.log.Warn().Err(err).Int64("connection_id", connID).Msg("appsync: failed to record sync result")
	}
}

// feedURL assembles the absolute per-slug Torznab feed URL the app will poll.
func feedURL(base, slug string) string {
	return strings.TrimRight(base, "/") + feedURLMarker + url.PathEscape(slug) + "/results/torznab"
}

// pushStatus maps a reconcile action to a stored per-indexer status.
func pushStatus(action string) string {
	if action == ActionFailed {
		return domain.SyncStatusError
	}
	return domain.SyncStatusOK
}

// summaryError joins the scrubbed errors of failed indexers for the connection-level
// last_sync_error.
func summaryError(outcomes []IndexerOutcome) string {
	var parts []string
	for _, o := range outcomes {
		if o.Action == ActionFailed {
			parts = append(parts, o.Slug+": "+apphttp.RedactError(o.Err))
		}
	}
	return strings.Join(parts, "; ")
}

// toResults converts outcomes into the scrubbed report view.
func toResults(outcomes []IndexerOutcome) []SyncResult {
	out := make([]SyncResult, 0, len(outcomes))
	for _, o := range outcomes {
		out = append(out, SyncResult{Slug: o.Slug, Action: o.Action, Error: apphttp.RedactError(o.Err)})
	}
	return out
}

// slugByID / idBySlug / selectedByID / priorBySlug are the lookup helpers reconcile and
// persistence need from the instance list and ledger.
func slugByID(instances []domain.IndexerInstance) map[int64]string {
	m := make(map[int64]string, len(instances))
	for _, inst := range instances {
		m[inst.ID] = inst.Slug
	}
	return m
}

func idBySlug(instances []domain.IndexerInstance) map[string]int64 {
	m := make(map[string]int64, len(instances))
	for _, inst := range instances {
		m[inst.Slug] = inst.ID
	}
	return m
}

func selectedByID(ledger []domain.AppConnectionIndexer) map[int64]bool {
	m := make(map[int64]bool, len(ledger))
	for _, l := range ledger {
		m[l.InstanceID] = l.Selected
	}
	return m
}

func priorBySlug(ledger []domain.AppConnectionIndexer, slugByID map[int64]string) map[string]LedgerEntry {
	m := make(map[string]LedgerEntry, len(ledger))
	for _, l := range ledger {
		slug, ok := slugByID[l.InstanceID]
		if !ok {
			continue
		}
		m[slug] = LedgerEntry{RemoteID: l.RemoteID, PayloadHash: l.PayloadHash}
	}
	return m
}
