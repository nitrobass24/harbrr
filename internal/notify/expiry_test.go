package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// scanFixture is the expiry scan under test wired to a real (in-memory) database and a
// counting webhook target, so every assertion is about persisted state and real sends
// rather than mocks. now is mutable: advancing it is how a test moves the clock across
// a threshold, which is the only thing that can trigger this feature.
type scanFixture struct {
	t       *testing.T
	svc     *Service
	db      *database.DB
	scanner *ExpiryScanner
	sends   *int64
	now     time.Time
	// mu guards last, the most recent payload the webhook target received. The send
	// happens on the httptest server's goroutine, so reading it needs the lock.
	mu   sync.Mutex
	last webhookPayload
}

// recordingServer answers 204, counts requests and keeps the last decoded payload, so a
// test can assert on what a transport ACTUALLY received rather than on a reconstruction.
func (f *scanFixture) recordingServer() *httptest.Server {
	f.t.Helper()
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		var p webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err == nil {
			f.mu.Lock()
			f.last = p
			f.mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	f.t.Cleanup(srv.Close)
	f.sends = &n
	return srv
}

// lastPayload returns the most recent payload the target received.
func (f *scanFixture) lastPayload() webhookPayload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func newScanFixture(t *testing.T) *scanFixture {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	f := &scanFixture{t: t, db: db, now: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)}
	srv := f.recordingServer()
	f.svc = NewService(db, kr, http.DefaultClient, zerolog.Nop())
	f.svc.clock = func() time.Time { return f.now }
	if _, err := f.svc.CreateNotification(context.Background(), CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeWebhook, URL: srv.URL,
	}); err != nil {
		t.Fatalf("create target: %v", err)
	}
	f.scanner = NewExpiryScanner(f.svc, db, "https://harbrr.example/indexers", func() time.Time { return f.now })
	return f
}

// addIndexer inserts an instance carrying the given expiry state.
func (f *scanFixture) addIndexer(slug, expiresAt, kind string, lifetime bool) int64 {
	f.t.Helper()
	id, err := (database.Instances{}).Insert(context.Background(), f.db, domain.IndexerInstance{
		Slug: slug, DefinitionID: slug, Name: slug, Protocol: "torrent", Enabled: true, Priority: 25,
		ExpiresAt: expiresAt, ExpiryKind: kind, ExpiryLifetime: lifetime,
		CreatedAt: f.now, UpdatedAt: f.now,
	})
	if err != nil {
		f.t.Fatalf("insert instance %q: %v", slug, err)
	}
	return id
}

// setExpiresAt moves an instance's expiry date the way a renewal in the UI would (the
// update path never touches the notification ledger — that is the point of keying it
// on the date).
func (f *scanFixture) setExpiresAt(id int64, date string) {
	f.t.Helper()
	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE indexer_instances SET expires_at = ? WHERE id = ?`, date, id); err != nil {
		f.t.Fatalf("set expires_at: %v", err)
	}
}

// ledger reads an instance's persisted fired-state.
func (f *scanFixture) ledger(id int64) (forDate string, days int) {
	f.t.Helper()
	if err := f.db.QueryRowContext(context.Background(),
		`SELECT expiry_notified_for, expiry_notified_days FROM indexer_instances WHERE id = ?`, id).
		Scan(&forDate, &days); err != nil {
		f.t.Fatalf("read ledger: %v", err)
	}
	return forDate, days
}

// tick runs one scan and asserts the cumulative number of sends afterwards. dispatch is
// synchronous inside TickOnce, so the count is settled by the time this returns.
func (f *scanFixture) tick(wantSends int64) {
	f.t.Helper()
	f.scanner.TickOnce(context.Background())
	if got := atomic.LoadInt64(f.sends); got != wantSends {
		f.t.Fatalf("cumulative sends = %d, want %d", got, wantSends)
	}
}

// at moves the fixture clock to a calendar date.
func (f *scanFixture) at(date string) {
	f.t.Helper()
	d, err := time.Parse(domain.ExpiryDateLayout, date)
	if err != nil {
		f.t.Fatalf("bad test date %q: %v", date, err)
	}
	f.now = d.Add(12 * time.Hour)
}

// TestExpiryScanFiresEachThresholdExactlyOnce walks a single indexer down the whole
// ladder: every threshold produces exactly one notification no matter how many times
// the scan runs in between, and the at-expiry warning is not followed by a daily
// repeat once the date has passed.
func TestExpiryScanFiresEachThresholdExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	id := f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindPerk, false)

	steps := []struct {
		name      string
		date      string
		wantSends int64
		wantDays  int
	}{
		{name: "40 days out — beyond every lead time", date: "2026-06-22", wantSends: 0, wantDays: 0},
		{name: "30 days out — first warning", date: "2026-07-02", wantSends: 1, wantDays: 30},
		{name: "29 days out — no repeat", date: "2026-07-03", wantSends: 1, wantDays: 30},
		{name: "14 days out", date: "2026-07-18", wantSends: 2, wantDays: 14},
		{name: "7 days out", date: "2026-07-25", wantSends: 3, wantDays: 7},
		{name: "1 day out", date: "2026-07-31", wantSends: 4, wantDays: 1},
		{name: "expiry day", date: "2026-08-01", wantSends: 5, wantDays: 0},
		{name: "a day past — expired must not re-notify", date: "2026-08-02", wantSends: 5, wantDays: 0},
		{name: "a month past — still silent", date: "2026-09-01", wantSends: 5, wantDays: 0},
	}
	// An explicit sequential walk, deliberately NOT t.Run subtests: every step mutates
	// the same fixture, so a filtered subtest run (-run .../expiry_day) would skip the
	// earlier transitions and assert against virgin state. Step names ride the errors.
	for _, step := range steps {
		f.at(step.date)
		f.tick(step.wantSends)
		f.tick(step.wantSends) // a second scan in the same day changes nothing
		gotFor, gotDays := f.ledger(id)
		if step.wantSends == 0 {
			if gotFor != "" {
				t.Errorf("%s: ledger armed early: notified_for = %q", step.name, gotFor)
			}
			continue
		}
		if gotFor != "2026-08-01" || gotDays != step.wantDays {
			t.Errorf("%s: ledger = (%q, %d), want (%q, %d)", step.name, gotFor, gotDays, "2026-08-01", step.wantDays)
		}
	}
}

// TestExpiryScanReArmsOnDateChange is the renewal case: a new date invalidates the
// ledger without any reset write, so the ladder starts over.
func TestExpiryScanReArmsOnDateChange(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	id := f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindPerk, false)

	f.at("2026-07-28") // 4 days out: the 7-day warning fires
	f.tick(1)
	f.tick(1)

	f.setExpiresAt(id, "2027-08-01") // renewed for a year
	f.tick(1)                        // now far out again — nothing due
	if gotFor, _ := f.ledger(id); gotFor != "2026-08-01" {
		t.Errorf("ledger rewritten by a re-arm: %q", gotFor)
	}

	f.at("2027-07-28") // same 4-days-out point on the NEW date
	f.tick(2)
	gotFor, gotDays := f.ledger(id)
	if gotFor != "2027-08-01" || gotDays != 7 {
		t.Errorf("re-armed ledger = (%q, %d), want (%q, 7)", gotFor, gotDays, "2027-08-01")
	}
}

// TestExpiryScanSurvivesRestart proves the fired state is persisted, not in-memory: a
// brand-new scanner over the same database re-sends nothing.
func TestExpiryScanSurvivesRestart(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindAccount, false)

	f.at("2026-07-25")
	f.tick(1)

	f.scanner = NewExpiryScanner(f.svc, f.db, "", func() time.Time { return f.now })
	f.tick(1)
}

// TestExpiryScanSkipsUntrackedAndLifetime covers the two never-fire cases plus the
// empty-fleet no-op — an installation that tracks nothing must stay completely silent.
func TestExpiryScanSkipsUntrackedAndLifetime(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)

	f.at("2026-08-01")
	f.tick(0) // no indexers at all

	f.addIndexer("untracked", "", "", false)
	f.addIndexer("lifetime", "2026-08-01", domain.ExpiryKindPerk, true)
	f.at("2026-09-01") // long past the lifetime indexer's (ignored) date
	f.tick(0)
	f.tick(0)
}

// TestExpiryScanCollapsesMissedThresholds is the offline case: harbrr down from 30 days
// out to 5 must send ONE message describing today, not a backlog of three.
func TestExpiryScanCollapsesMissedThresholds(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	id := f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindPerk, false)

	f.at("2026-07-27") // 5 days out — 30, 14 and 7 are all due
	f.tick(1)
	if _, gotDays := f.ledger(id); gotDays != 7 {
		t.Errorf("ledger days = %d, want 7 (the most urgent due threshold)", gotDays)
	}
}

// TestExpiryScanRespectsOptOut proves the event rides the normal per-target opt-in and
// is not entangled with the health flag.
func TestExpiryScanRespectsOptOut(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	list, err := f.svc.ListNotifications(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list targets: %v (%d)", err, len(list))
	}
	off := false
	if err := f.svc.UpdateNotification(context.Background(), list[0].ID, UpdateNotificationParams{OnExpiry: &off}); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindPerk, false)
	f.at("2026-08-01")
	f.tick(0)
}

// TestExpiryScanCustomThresholds proves the operator's stored lead times are what the
// scan applies — including that the at-expiry warning survives a list that omits it.
func TestExpiryScanCustomThresholds(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	if _, err := f.svc.SetExpiryThresholds(context.Background(), []int{60}); err != nil {
		t.Fatalf("set thresholds: %v", err)
	}
	f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindPerk, false)

	f.at("2026-07-25") // 7 days out: not a threshold any more, but 60 already passed
	f.tick(1)
	f.at("2026-07-31") // 1 day out is no longer a threshold
	f.tick(1)
	f.at("2026-08-01") // at-expiry always fires
	f.tick(2)
}

func TestSetExpiryThresholdsCanonicalizes(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		in      []int
		want    []int
		wantErr bool
	}{
		{name: "sorts, dedupes and appends the at-expiry warning", in: []int{7, 30, 7}, want: []int{30, 7, 0}},
		{name: "empty leaves only the at-expiry warning", in: []int{}, want: []int{0}},
		{name: "an explicit 0 is not duplicated", in: []int{0, 3}, want: []int{3, 0}},
		{name: "negative is rejected", in: []int{-1}, wantErr: true},
		{name: "absurdly long lead time is rejected", in: []int{4000}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.SetExpiryThresholds(ctx, tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SetExpiryThresholds(%v) = %v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetExpiryThresholds(%v): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SetExpiryThresholds(%v) = %v, want %v", tt.in, got, tt.want)
			}
			readBack, err := svc.ExpiryThresholds(ctx)
			if err != nil {
				t.Fatalf("ExpiryThresholds: %v", err)
			}
			if !reflect.DeepEqual(readBack, tt.want) {
				t.Errorf("read back = %v, want %v", readBack, tt.want)
			}
		})
	}
}

func TestExpiryThresholdsFallBackToDefaults(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	got, err := svc.ExpiryThresholds(ctx)
	if err != nil {
		t.Fatalf("ExpiryThresholds: %v", err)
	}
	if !reflect.DeepEqual(got, defaultExpiryThresholds) {
		t.Errorf("unset = %v, want the defaults %v", got, defaultExpiryThresholds)
	}
	// A junk row (hand-edited, or written by a future version) must not silence the
	// scan — it falls back rather than yielding an empty ladder.
	if err := (database.AppSettings{}).Set(ctx, svc.db, ExpiryThresholdsKey, "nonsense", svc.clock()); err != nil {
		t.Fatalf("seed junk: %v", err)
	}
	got, err = svc.ExpiryThresholds(ctx)
	if err != nil {
		t.Fatalf("ExpiryThresholds: %v", err)
	}
	if !reflect.DeepEqual(got, defaultExpiryThresholds) {
		t.Errorf("junk row = %v, want the defaults %v", got, defaultExpiryThresholds)
	}
}

func TestDefaultExpiryThresholdsIsACopy(t *testing.T) {
	t.Parallel()
	got := DefaultExpiryThresholds()
	got[0] = -999
	if defaultExpiryThresholds[0] != 30 {
		t.Errorf("the package defaults were mutated through the accessor: %v", defaultExpiryThresholds)
	}
}

func TestDueThreshold(t *testing.T) {
	t.Parallel()
	thresholds := []int{30, 14, 7, 1, 0}

	tests := []struct {
		name  string
		days  int
		want  int
		wantK bool
	}{
		{name: "beyond every lead time", days: 31},
		{name: "exactly the widest", days: 30, want: 30, wantK: true},
		// 20 days out, only the 30-day warning has been reached — the 14-day one is
		// still in the future and must NOT be pulled forward.
		{name: "between two", days: 20, want: 30, wantK: true},
		{name: "the one-day warning is not swallowed", days: 1, want: 1, wantK: true},
		{name: "expiry day", days: 0, want: 0, wantK: true},
		{name: "already expired", days: -12, want: 0, wantK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := dueThreshold(thresholds, tt.days)
			if ok != tt.wantK || (ok && got != tt.want) {
				t.Errorf("dueThreshold(%d) = (%d, %v), want (%d, %v)", tt.days, got, ok, tt.want, tt.wantK)
			}
		})
	}
}

func TestDaysUntil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		now  time.Time
		date string
		want int
		ok   bool
	}{
		{name: "future", now: time.Date(2026, 7, 1, 23, 59, 0, 0, time.UTC), date: "2026-07-08", want: 7, ok: true},
		{name: "same day, late in the evening, is still 0", now: time.Date(2026, 7, 8, 23, 59, 0, 0, time.UTC), date: "2026-07-08", ok: true},
		{name: "past", now: time.Date(2026, 7, 10, 0, 1, 0, 0, time.UTC), date: "2026-07-08", want: -2, ok: true},
		{name: "across a DST boundary in a non-UTC zone", now: time.Date(2026, 3, 27, 9, 0, 0, 0, time.FixedZone("CET", 3600)), date: "2026-03-30", want: 3, ok: true},
		{name: "unparseable", now: time.Now(), date: "next tuesday"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := daysUntil(tt.now, tt.date)
			if ok != tt.ok || got != tt.want {
				t.Errorf("daysUntil(%v, %q) = (%d, %v), want (%d, %v)", tt.now, tt.date, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestExpiryDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind string
		days int
		link string
		want string
	}{
		{
			name: "perk, a week out, with a link",
			kind: domain.ExpiryKindPerk, days: 7, link: "https://harbrr.example/indexers",
			want: "VIP/perk expires in 7 days (2026-08-01). Renew on the tracker, then update the date in harbrr: https://harbrr.example/indexers.",
		},
		{
			name: "account, tomorrow, no external URL configured",
			kind: domain.ExpiryKindAccount, days: 1,
			want: "Account expires tomorrow (2026-08-01). Renew on the tracker, then update the date in harbrr.",
		},
		{
			name: "unset kind reads generically rather than guessing",
			kind: "", days: 0,
			want: "Membership expires today (2026-08-01). Renew on the tracker, then update the date in harbrr.",
		},
		{
			name: "already expired says so loudly",
			kind: domain.ExpiryKindPerk, days: -3,
			want: "VIP/perk EXPIRED 2026-08-01, 3 days ago. Renew on the tracker, then update the date in harbrr.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expiryDetail(tt.kind, "2026-08-01", tt.days, tt.link); got != tt.want {
				t.Errorf("expiryDetail = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExpiryEventShape pins what a transport actually receives: the untouched webhook
// payload must name the event kind, the indexer, the expiry kind, and a detail carrying
// the date and the deep link — the transports were not changed for this feature, so the
// Event fields are the entire contract.
func TestExpiryEventShape(t *testing.T) {
	t.Parallel()
	f := newScanFixture(t)
	f.addIndexer("orpheus", "2026-08-01", domain.ExpiryKindAccount, false)
	f.at("2026-07-25")
	f.tick(1)

	got := f.lastPayload()
	if got.Event != EventIndexerExpiry {
		t.Errorf("event = %q, want %q", got.Event, EventIndexerExpiry)
	}
	if got.Indexer != "orpheus" || got.Kind != domain.ExpiryKindAccount {
		t.Errorf("indexer/kind = %q/%q, want orpheus/%s", got.Indexer, got.Kind, domain.ExpiryKindAccount)
	}
	for _, want := range []string{"2026-08-01", "7 days", "https://harbrr.example/indexers"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail %q is missing %q", got.Detail, want)
		}
	}
	if !got.Timestamp.Equal(f.now) {
		t.Errorf("timestamp = %v, want the scan clock %v", got.Timestamp, f.now)
	}
}
