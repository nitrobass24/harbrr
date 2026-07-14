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

// ConnectionSyncResult is one connection's outcome in a SyncAll run: the connection
// identity plus either its SyncReport or a scrubbed error string if that connection
// failed (a single failure never aborts the others).
type ConnectionSyncResult struct {
	ConnectionID int64
	Name         string
	Report       SyncReport
	Error        string
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
		return SyncReport{}, fmt.Errorf("%w: %s", domain.ErrInvalid, detail)
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

	profile, err := s.connProfile(ctx, conn)
	if err != nil {
		return SyncReport{}, err
	}
	desired, err := s.buildDesired(ctx, instances, conn, harbrrKey, selectedByID(ledger), profile)
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

// SyncAll reconciles every configured connection in one pass. A single connection's
// failure is captured (scrubbed) and never aborts the rest; disabled connections
// self-skip inside Sync (StatusSkipped, no remote calls) — matching Prowlarr, a paused
// app is never pushed to. Only a failure to enumerate the connections aborts. Sync
// returns the raw error, so SyncAll scrubs it before it crosses the API boundary.
func (s *Service) SyncAll(ctx context.Context) ([]ConnectionSyncResult, error) {
	conns, err := s.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionSyncResult, 0, len(conns))
	for _, conn := range conns {
		res := ConnectionSyncResult{ConnectionID: conn.ID, Name: conn.Name}
		if report, err := s.Sync(ctx, conn.ID); err != nil {
			res.Error = apphttp.RedactError(err)
		} else {
			res.Report = report
		}
		out = append(out, res)
	}
	return out, nil
}

// buildDesired projects every in-scope indexer into a DesiredIndexer: the per-app feed
// URL, the connection's harbrr key, and the (gated) categories. Scope "selected" keeps
// only indexers flagged in the ledger. A non-nil profile narrows the pushed categories
// (within the app's content type) and overrides the min-seeders floor and search-mode
// toggles; nil profile is exactly today's behavior.
func (s *Service) buildDesired(ctx context.Context, instances []domain.IndexerInstance, conn domain.AppConnection, harbrrKey string, selected map[int64]bool, profile *domain.SyncProfile) ([]DesiredIndexer, error) {
	out := make([]DesiredIndexer, 0, len(instances))
	for _, inst := range instances {
		if conn.IndexScope == domain.IndexScopeSelected && !selected[inst.ID] {
			continue
		}
		// qui is a torrent-only Torznab consumer (POST /api/torznab/indexers); it has no
		// usenet/Newznab notion, so a usenet indexer is never pushed to it.
		if conn.Kind == domain.AppKindQui && inst.Protocol == servarrUsenetProtocol {
			continue
		}
		cats, err := s.source.Categories(ctx, inst.Slug)
		if err != nil {
			return nil, fmt.Errorf("appsync: categories for %q: %w", inst.Slug, err)
		}
		// The content gate (kind range, optionally narrowed by the profile) both decides
		// whether the indexer qualifies and produces the exact category set to push.
		gated, ok := gateCategories(conn.Kind, profile, cats)
		if !ok {
			continue
		}
		caps, err := s.source.Capabilities(ctx, inst.Slug)
		if err != nil {
			return nil, fmt.Errorf("appsync: capabilities for %q: %w", inst.Slug, err)
		}
		rss, auto, interactive := resolveToggles(inst.Enabled, profile)
		out = append(out, DesiredIndexer{
			Slug: inst.Slug, Name: inst.Name, FeedURL: FeedURL(conn.HarbrrURL, inst.Slug, conn.FreeleechMode),
			APIKey: harbrrKey, Categories: gated, Capabilities: caps,
			Priority: conn.Priority, Enabled: inst.Enabled, Protocol: inst.Protocol,
			EnableRss: rss, EnableAutomaticSearch: auto, EnableInteractiveSearch: interactive,
			MinSeeders: profileMinSeeders(profile),
		})
	}
	return out, nil
}

// connProfile loads the sync profile a connection references, or nil when it has none or
// is a qui connection (profiles never apply to qui — validation blocks the assignment, so
// the kind check here is defensive). Loaded once per Sync and threaded into buildDesired.
func (s *Service) connProfile(ctx context.Context, conn domain.AppConnection) (*domain.SyncProfile, error) {
	if conn.SyncProfileID == nil || conn.Kind == domain.AppKindQui {
		return nil, nil //nolint:nilnil // "no profile" is a valid, non-error outcome (today's default behavior).
	}
	profile, err := s.profiles.GetProfile(ctx, s.db, *conn.SyncProfileID)
	if err != nil {
		return nil, fmt.Errorf("appsync: load sync profile: %w", err)
	}
	return &profile, nil
}

// gateCategories applies the connection's content gate to an indexer's categories and
// returns the exact set to push. With no profile (or an empty profile category set) it is
// the existing kind gate: keep the full set iff IndexerServesApp is true (today's
// behavior). A non-empty profile set narrows to the intersection of categories the app
// serves AND the profile selects; the indexer qualifies only if that intersection is
// non-empty. This is narrow-only — a profile can exclude categories but never cross the
// app's content type (a books tracker never reaches Sonarr). The returned slice preserves
// Category.Name so Sonarr's animeCategories still resolve.
func gateCategories(kind string, profile *domain.SyncProfile, cats []Category) ([]Category, bool) {
	if profile == nil || len(profile.Categories) == 0 {
		if !IndexerServesApp(kind, cats) {
			return nil, false
		}
		return cats, true
	}
	filtered := make([]Category, 0, len(cats))
	for _, c := range cats {
		if categoryServesApp(kind, c.ID) && profileMatch(profile.Categories, c.ID) {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil, false
	}
	return filtered, true
}

// profileMatch reports whether a profile's category-id set covers catID. A value that is
// a multiple of 1000 is a parent covering the whole [v, v+999] block (e.g. 2000 covers
// 2040); any other value matches exactly (e.g. 3030 matches only 3030). This mirrors the
// Newznab parent/child convention the category-picker UI produces.
func profileMatch(ids []int, catID int) bool {
	for _, v := range ids {
		if v == catID {
			return true
		}
		if v%1000 == 0 && catID >= v && catID < v+1000 {
			return true
		}
	}
	return false
}

// resolveToggles combines an instance's enabled state with a profile's per-mode toggles:
// each pushed flag is enabled AND the profile's matching toggle (no profile → all three
// equal enabled). A disabled instance therefore forces every flag false regardless of the
// profile — the instance's own state always wins.
func resolveToggles(enabled bool, p *domain.SyncProfile) (rss, auto, interactive bool) {
	if p == nil {
		return enabled, enabled, enabled
	}
	return enabled && p.EnableRss, enabled && p.EnableAutomaticSearch, enabled && p.EnableInteractiveSearch
}

// profileMinSeeders is the connection's pushed minimum-seeders floor: the profile's value,
// or 0 (unset → the app default) when there is no profile.
func profileMinSeeders(p *domain.SyncProfile) int {
	if p == nil {
		return 0
	}
	return p.MinSeeders
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

// FeedURL assembles the absolute per-slug Torznab feed URL the app will poll. A bypass
// connection gets the /full variant (the full catalog, freeleech view skipped); honor
// (the default for *arrs) gets the standard feed that respects the indexer's freeleech
// setting. The slug is recovered from the URL path by slugFromFeedURL regardless of the
// trailing /full, so orphan-detection still matches harbrr-managed rows. Exported so the
// smoke harness asserts the live feed URL matches the expected shape (single source of truth).
func FeedURL(base, slug, freeleechMode string) string {
	u := strings.TrimRight(base, "/") + feedURLMarker + url.PathEscape(slug) + "/results/torznab"
	if freeleechMode == domain.FreeleechModeBypass {
		u += "/full"
	}
	return u
}

// AppCategoryRange returns the inclusive Newznab category range a Servarr app kind
// consumes. ok is false for kinds with no content-type notion (qui, and — defensively —
// any kind that isn't a known Servarr; connections are validated in validate.go, so an
// unknown kind is unreachable and treated as "no filter, push everything").
func AppCategoryRange(kind string) (lo, hi int, ok bool) {
	switch kind {
	case domain.AppKindRadarr: // Movies
		return 2000, 2999, true
	case domain.AppKindLidarr: // Music
		return 3000, 3999, true
	case domain.AppKindSonarr: // TV
		return 5000, 5999, true
	case domain.AppKindWhisparr: // Adult
		return 6000, 6999, true
	case domain.AppKindReadarr: // Books
		return 7000, 7999, true
	default: // qui (and unknown) — general Torznab pool, no content-type filter
		return 0, 0, false
	}
}

// IndexerServesApp reports whether an indexer belongs on a connection of this kind: a
// Servarr kind requires >=1 category in its Newznab range; qui (no range) always serves.
// Custom categories (>=100000) fall in no Servarr range, so they never qualify an
// indexer for a Servarr app on their own.
// audiobookCategory (Audio/Audiobook) is the one Newznab category that crosses the
// simple round-thousand ranges: Prowlarr syncs it to BOTH Lidarr (via its 3000s range)
// AND Readarr, even though 3030 sits outside Readarr's 7000s Books range. Readarr
// accepts it as an extra so an audiobook-only tracker still reaches Readarr (parity).
const audiobookCategory = 3030

func IndexerServesApp(kind string, cats []Category) bool {
	lo, hi, ok := AppCategoryRange(kind)
	if !ok {
		return true
	}
	for _, c := range cats {
		if c.ID >= lo && c.ID <= hi {
			return true
		}
		if kind == domain.AppKindReadarr && c.ID == audiobookCategory {
			return true
		}
	}
	return false
}

// categoryServesApp is the single-category form of IndexerServesApp: whether one Newznab
// category id belongs to a Servarr app of this kind (within its content range, or the
// Readarr audiobook special-case). qui (no range) serves everything. Shared by the
// profile category gate (buildDesired) and the attach-time overlap guard (validateProfileRef).
func categoryServesApp(kind string, id int) bool {
	lo, hi, ok := AppCategoryRange(kind)
	if !ok {
		return true
	}
	if id >= lo && id <= hi {
		return true
	}
	return kind == domain.AppKindReadarr && id == audiobookCategory
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
