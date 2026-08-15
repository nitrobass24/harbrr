package notify

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// ExpiryScanInterval is how often the expiry scan runs. Thresholds are measured in
// DAYS, so hourly is already an order of magnitude finer than it needs to be; it
// exists at this rate only so a freshly-entered date that is already inside a
// threshold surfaces the same day rather than the next.
const ExpiryScanInterval = time.Hour

// ExpiryThresholdsKey is the app_settings key holding the operator's lead times, a
// comma-separated list of days ("30,14,7,1"). Absent or unparseable means
// defaultExpiryThresholds — a stale row can never silence the feature.
const ExpiryThresholdsKey = "expiry.thresholds"

// defaultExpiryThresholds are the lead times (days before expiry) a warning fires at.
// 0 is the at-expiry warning and is ALWAYS present, appended to whatever the operator
// configures: an expiry that passes unannounced is the exact failure #399 exists to
// prevent, so it is not something a bad thresholds value can switch off.
var defaultExpiryThresholds = []int{30, 14, 7, 1, 0}

// DefaultExpiryThresholds returns a copy of the default lead times for the settings
// endpoint to report when the operator has set none. A copy, not the slice: the
// package-level defaults are the fallback every scan reads, and a caller that sorted
// or appended to them in place would silently retune the feature.
func DefaultExpiryThresholds() []int {
	return append([]int(nil), defaultExpiryThresholds...)
}

// ExpiryScanner turns operator-entered expiry dates into notifications. It is a pull,
// not a push: nothing about an expiry happens during a request, so a periodic scan on
// the shared reap skeleton (internal/app/lifecycle.go) is the whole mechanism — no
// tracker traffic, no probing, just a date and a clock.
//
// It deliberately does NOT use healthSuppressed. That debounce collapses a REPEATING
// condition inside a time window; an expiry is a countdown, and a window suppressor
// would either re-send the same warning daily or swallow the 1-day one entirely.
// Exactly-once instead comes from the persisted (expiry date, most urgent threshold
// fired) ledger on the instance row, which survives a restart and re-arms by itself
// when the date changes.
type ExpiryScanner struct {
	svc       *Service
	db        dbinterface.Querier
	instances database.Instances
	clock     func() time.Time
	// link is the externally-visible URL of harbrr's indexers page — where the date is
	// one field away from renewed. Empty when no external URL is configured, in which
	// case the notification just says what to do without a link.
	link string
}

// NewExpiryScanner wires the scan to the notify service that dispatches its events.
// clock is injectable for deterministic tests; nil uses the service's own clock.
func NewExpiryScanner(svc *Service, db dbinterface.Querier, link string, clock func() time.Time) *ExpiryScanner {
	if clock == nil {
		clock = svc.clock
	}
	return &ExpiryScanner{svc: svc, db: db, clock: clock, link: link}
}

// TickOnce runs one scan: every indexer with a tracked (non-lifetime, non-empty)
// expiry date is compared against the configured thresholds, and each threshold that
// has become due since the last scan dispatches one event. An installation that tracks
// no expiries costs one indexed-scan-free SELECT returning zero rows and stops there —
// the thresholds are not even read.
func (s *ExpiryScanner) TickOnce(ctx context.Context) {
	rows, err := s.instances.ListExpiryTracked(ctx, s.db)
	if err != nil {
		s.svc.log.Warn().Str("error", apphttp.RedactError(err)).Msg("notify: expiry scan: list tracked expiries failed")
		return
	}
	if len(rows) == 0 {
		return
	}
	thresholds := s.thresholds(ctx)
	now := s.clock()
	for _, row := range rows {
		s.check(ctx, row, thresholds, now)
	}
}

// check fires at most one event for one indexer: the most urgent threshold now due,
// skipped when the ledger says that threshold (or a more urgent one) already fired for
// this same expiry date. Collapsing to the most urgent due threshold is what keeps a
// harbrr that was offline across 30/14/7 days from sending three messages at once —
// the operator gets the one that is actually true today.
func (s *ExpiryScanner) check(ctx context.Context, row domain.IndexerExpiry, thresholds []int, now time.Time) {
	days, ok := daysUntil(now, row.ExpiresAt)
	if !ok {
		s.svc.log.Warn().Str("indexer", row.Slug).Msg("notify: expiry scan: unparseable expiry date, skipped")
		return
	}
	due, ok := dueThreshold(thresholds, days)
	if !ok {
		return // still further out than the earliest lead time
	}
	if row.NotifiedFor == row.ExpiresAt && row.NotifiedDays <= due {
		return // already warned at this urgency (or a sharper one) for this date
	}
	// Record BEFORE dispatching. A send is best-effort and unacknowledged, so the
	// ledger cannot be conditioned on it; marking first makes a failed write cost one
	// notification instead of turning every future scan into a repeat of the same one.
	if err := s.instances.MarkExpiryNotified(ctx, s.db, row.InstanceID, row.ExpiresAt, due); err != nil {
		s.svc.log.Warn().Str("indexer", row.Slug).Str("error", apphttp.RedactError(err)).
			Msg("notify: expiry scan: recording the fired threshold failed; not dispatching")
		return
	}
	s.svc.dispatch(ctx, Event{
		Event:     EventIndexerExpiry,
		Indexer:   row.Slug,
		Kind:      row.Kind,
		Detail:    expiryDetail(row.Kind, row.ExpiresAt, days, s.link),
		Timestamp: now,
	}, func(n domain.Notification) bool { return n.OnExpiry })
}

// thresholds reads the operator's lead times, falling back to the defaults — a
// settings read that fails must not silence the scan, so the error is logged and the
// defaults stand in.
func (s *ExpiryScanner) thresholds(ctx context.Context) []int {
	days, err := s.svc.ExpiryThresholds(ctx)
	if err != nil {
		s.svc.log.Warn().Str("error", apphttp.RedactError(err)).Msg("notify: expiry scan: reading thresholds failed")
		return DefaultExpiryThresholds()
	}
	return days
}

// ExpiryThresholds returns the effective lead times: the operator's stored list, or
// the defaults when unset or unusable. Both the scan and the settings endpoint read
// through here, so what the UI shows is what the scan applies.
func (s *Service) ExpiryThresholds(ctx context.Context) ([]int, error) {
	raw, found, err := (database.AppSettings{}).Get(ctx, s.db, ExpiryThresholdsKey)
	if err != nil {
		return nil, fmt.Errorf("notify: read expiry thresholds: %w", err)
	}
	if found {
		if parsed := ParseExpiryThresholds(raw); len(parsed) > 0 {
			return parsed, nil
		}
	}
	return DefaultExpiryThresholds(), nil
}

// maxExpiryLeadDays bounds a lead time at ten years — beyond that a "warning" would
// fire the instant the date is entered and carry no information.
const maxExpiryLeadDays = 3650

// SetExpiryThresholds validates and persists the lead times, returning the CANONICAL
// list actually stored (deduped, descending, with the mandatory at-expiry 0 appended).
// Storing the canonical form rather than the raw input means a read-back can never
// disagree with what the scan does. An empty or all-zero list is not an error — it
// leaves exactly the at-expiry warning, which is a legitimate "only tell me when it
// actually lapses" choice.
func (s *Service) SetExpiryThresholds(ctx context.Context, days []int) ([]int, error) {
	for _, d := range days {
		if d < 0 || d > maxExpiryLeadDays {
			return nil, fmt.Errorf("%w: expiry lead times must be 0-%d days (got %d)", domain.ErrInvalid, maxExpiryLeadDays, d)
		}
	}
	canonical := ParseExpiryThresholds(formatThresholds(days))
	if len(canonical) == 0 {
		canonical = []int{0}
	}
	if err := (database.AppSettings{}).Set(ctx, s.db, ExpiryThresholdsKey, formatThresholds(canonical), s.clock()); err != nil {
		return nil, fmt.Errorf("notify: persist expiry thresholds: %w", err)
	}
	return canonical, nil
}

// formatThresholds renders a day list in the stored comma-separated form.
func formatThresholds(days []int) string {
	parts := make([]string, 0, len(days))
	for _, d := range days {
		parts = append(parts, strconv.Itoa(d))
	}
	return strings.Join(parts, ",")
}

// ParseExpiryThresholds parses a comma-separated lead-time list into a descending,
// deduped day list with the mandatory at-expiry (0) threshold appended. It returns nil
// when nothing usable is present, so a caller can fall back — and it is exported
// because the settings endpoint validates with the very same parser the scan uses,
// leaving no way to store a value that then reads back as something else.
func ParseExpiryThresholds(raw string) []int {
	seen := map[int]bool{}
	out := []int{}
	for field := range strings.SplitSeq(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	if !seen[0] {
		out = append(out, 0)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// daysUntil is the whole-day distance from now's calendar date to an expiry date
// (negative once it has passed). Both sides are parsed as bare dates, so the answer is
// a count of calendar days and no timezone or DST offset can shift it by one.
func daysUntil(now time.Time, date string) (int, bool) {
	target, err := time.Parse(domain.ExpiryDateLayout, date)
	if err != nil {
		return 0, false
	}
	today, err := time.Parse(domain.ExpiryDateLayout, now.Format(domain.ExpiryDateLayout))
	if err != nil {
		return 0, false
	}
	return int(target.Sub(today) / (24 * time.Hour)), true
}

// dueThreshold returns the most urgent (smallest) threshold that days has reached, and
// false when the expiry is still beyond every lead time. Because days only ever
// decreases as the clock runs, this value only ever gets more urgent for a given date —
// which is exactly why storing that single number is a complete fired-state ledger.
func dueThreshold(thresholds []int, days int) (int, bool) {
	best, found := 0, false
	for _, t := range thresholds {
		if days <= t && (!found || t < best) {
			best, found = t, true
		}
	}
	return best, found
}

// expiryLabel names what is ending. An unset kind reads generically rather than
// guessing: mislabelling an account lapse as a perk lapse understates it badly.
func expiryLabel(kind string) string {
	switch kind {
	case domain.ExpiryKindPerk:
		return "VIP/perk"
	case domain.ExpiryKindAccount:
		return "Account"
	default:
		return "Membership"
	}
}

// expiryDetail writes the notification body: what is expiring, when, and where to fix
// it. The link is the "actionable" part harbrr can honestly offer — it lands on the
// page where the date is editable. Nothing here is secret; a slug and a date are the
// only inputs.
func expiryDetail(kind, date string, days int, link string) string {
	var when string
	switch {
	case days > 1:
		when = fmt.Sprintf("expires in %d days (%s)", days, date)
	case days == 1:
		when = fmt.Sprintf("expires tomorrow (%s)", date)
	case days == 0:
		when = fmt.Sprintf("expires today (%s)", date)
	default:
		when = fmt.Sprintf("EXPIRED %s, %d days ago", date, -days)
	}
	action := "Renew on the tracker, then update the date in harbrr"
	if link != "" {
		action += ": " + link
	}
	return fmt.Sprintf("%s %s. %s.", expiryLabel(kind), when, action)
}
