package announce_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

func ptr[T any](v T) *T { return &v }

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// fakeTarget records the releases it was asked to announce and a fixed match verdict.
type fakeTarget struct {
	got      []announce.Release
	matched  bool
	err      error
	probeErr error
}

func (f *fakeTarget) Announce(_ context.Context, rel announce.Release) (announce.Result, error) {
	f.got = append(f.got, rel)
	if f.err != nil {
		return announce.Result{}, f.err
	}
	return announce.Result{Matched: f.matched}, nil
}

func (f *fakeTarget) Probe(context.Context) error { return f.probeErr }

func newService(t *testing.T, factory announce.TargetFactory) (*announce.Service, *database.DB, *apps.Service) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	appsSvc := apps.NewService(db, kr, http.DefaultClient, zerolog.Nop())
	return announce.NewService(db, appsSvc, auth.NewService(db), kr, factory, zerolog.Nop()), db, appsSvc
}

func TestServiceCreateGetDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, appsSvc := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return &fakeTarget{}, nil
	})

	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "qui_secret", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if conn.ID == 0 || conn.HarbrrAPIKeyID == 0 || conn.AppID == nil {
		t.Fatalf("connection not fully persisted: %+v", conn)
	}

	got, err := svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	// The tool credential is no longer sealed on the connection row — only the App owns
	// it now (AAD (app.ID, domain.AppSecret)).
	if got.APIKeyEncrypted != "" {
		t.Errorf("tool key sealed on the connection row, want it App-only: %q", got.APIKeyEncrypted)
	}
	app, err := appsSvc.Get(ctx, *conn.AppID)
	if err != nil {
		t.Fatalf("apps.Get: %v", err)
	}
	if app.APIKeyEncrypted == "" || app.APIKeyEncrypted == "qui_secret" {
		t.Errorf("tool key not encrypted at rest on the app: %q", app.APIKeyEncrypted)
	}
	toolKey, err := appsSvc.DecryptKey(app)
	if err != nil || toolKey != "qui_secret" {
		t.Errorf("DecryptKey(app) = %q, err %v, want the original tool key round-tripping", toolKey, err)
	}

	// the decrypted harbrr key (for the /dl link) round-trips and is not the ciphertext.
	hk, err := svc.HarbrrKey(got)
	if err != nil || hk == "" || hk == got.HarbrrAPIKeyEncrypted {
		t.Errorf("HarbrrKey = %q, err %v", hk, err)
	}

	if err := svc.DeleteConnection(ctx, conn.ID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	if _, err := svc.GetConnection(ctx, conn.ID); err == nil {
		t.Error("connection still present after delete")
	}
}

func TestServiceCreateValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	// cross-seed v6 requires a harbrr URL (it fetches the /dl link).
	_, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "cs", Kind: domain.AnnounceKindCrossSeedV6, BaseURL: "http://cs:2468", APIKey: "k",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("missing harbrrUrl err = %v, want ErrInvalid", err)
	}

	// unknown kind is rejected.
	_, err = svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "x", Kind: "sabnzbd", BaseURL: "http://x", APIKey: "k",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad kind err = %v, want ErrInvalid", err)
	}

	// a non-absolute / non-http base URL is rejected (would yield a host-less /dl link).
	_, err = svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "q", Kind: domain.AnnounceKindQui, BaseURL: "qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("relative base url err = %v, want ErrInvalid", err)
	}
}

// TestServiceCreateTrimsURLs proves whitespace-padded URLs are normalized before storage,
// so they can't bypass the (kind, base_url) uniqueness contract or leave a padded /dl URL.
func TestServiceCreateTrimsURLs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui,
		BaseURL: "  http://qui:7476  ", APIKey: "k", HarbrrURL: " http://h:8787 ",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	// Re-read through the service so the assertion proves AT-REST normalization, not just
	// the returned struct.
	got, err := svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.BaseURL != "http://qui:7476" || got.HarbrrURL != "http://h:8787" {
		t.Errorf("stored URLs not trimmed: baseURL=%q harbrrURL=%q", got.BaseURL, got.HarbrrURL)
	}

	// A second create with the UNPADDED same base URL must conflict — proof the padded one
	// was stored under its trimmed, canonical value (else the (kind, base_url) uniqueness
	// contract would not catch it).
	_, err = svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui2", Kind: domain.AnnounceKindQui,
		BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate (unpadded) base url err = %v, want ErrConflict", err)
	}
}

// TestServiceCreateReusesAppByID proves the AppID (reuse) create path: a second
// connection created with the first's AppID skips the inline identity fields entirely
// and both connections read back the same App's BaseURL/HarbrrURL — no second App row
// is created.
func TestServiceCreateReusesAppByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, appsSvc := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	// The App pre-exists — e.g. it was created by some other surface (app-sync) pointed
	// at the same qui instance — so this exercises the pure AppID (reuse) path, not
	// announce's own create-and-reference.
	app, err := appsSvc.Resolve(ctx, apps.Ref{
		Kind: domain.AnnounceKindQui, Name: "qui", BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("pre-create app: %v", err)
	}

	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui-conn", Kind: domain.AnnounceKindQui, AppID: &app.ID,
	})
	if err != nil {
		t.Fatalf("create by AppID: %v", err)
	}
	if conn.AppID == nil || *conn.AppID != app.ID {
		t.Fatalf("conn.AppID = %v, want the reused %v", conn.AppID, app.ID)
	}

	got, err := svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.BaseURL != "http://qui:7476" || got.HarbrrURL != "http://h:8787" {
		t.Errorf("connection not enriched from the reused App: %+v", got)
	}

	list, err := appsSvc.List(ctx)
	if err != nil {
		t.Fatalf("apps.List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("apps after AppID-reuse create = %d, want exactly 1 (no second App row)", len(list))
	}
}

// TestServiceListEnrichesFromApp proves ListConnections enriches each row's
// BaseURL/HarbrrURL from its App (the single read path), not from the row's own
// (now-blank) columns.
func TestServiceListEnrichesFromApp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := svc.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListConnections len = %d, want 1", len(list))
	}
	if list[0].BaseURL != "http://qui:7476" || list[0].HarbrrURL != "http://h:8787" {
		t.Errorf("ListConnections did not enrich from the App: %+v", list[0])
	}
}

// TestServicePendingAppMigration proves a row whose app_id is still NULL (the boot fold
// hasn't run yet) is refused with domain.ErrAppMigrationPending on a *use* path
// (TestConnection), and is skipped — not fatal — inside Push's best-effort fan-out.
func TestServicePendingAppMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pushed := 0
	svc, db, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) {
		pushed++
		return &fakeTarget{matched: true}, nil
	})

	repo := database.AnnounceConnections{}
	now := time.Now().UTC()
	id, err := repo.InsertAnnounceConnection(ctx, db, domain.AnnounceConnection{
		Name: "pending", Kind: domain.AnnounceKindQui, Enabled: true, // AppID left nil
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("insert pending row: %v", err)
	}

	if err := svc.TestConnection(ctx, id); !errors.Is(err, domain.ErrAppMigrationPending) {
		t.Errorf("TestConnection(pending) err = %v, want ErrAppMigrationPending", err)
	}

	matched := svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release {
		return []announce.Release{{Name: "X", GUID: "g1"}}
	})
	if matched != 0 || pushed != 0 {
		t.Errorf("Push(pending row) matched=%d pushed=%d, want 0/0 (skipped, not fatal)", matched, pushed)
	}
}

// TestServiceHarbrrKeyRejectsRevoked proves HarbrrKey refuses a connection whose minted key
// was revoked out of band (FK SET NULL → id 0), so a dead /dl signing key is never used.
func TestServiceHarbrrKeyRejectsRevoked(t *testing.T) {
	t.Parallel()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })
	_, err := svc.HarbrrKey(domain.AnnounceConnection{ID: 1, HarbrrAPIKeyID: 0})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("HarbrrKey(revoked) err = %v, want ErrInvalid", err)
	}
}

func TestServicePushFansOutToEnabledOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targets := map[int64]*fakeTarget{}
	svc, _, _ := newService(t, func(conn domain.AnnounceConnection, _ string) (announce.Target, error) {
		tgt := &fakeTarget{matched: true}
		targets[conn.ID] = tgt
		return tgt, nil
	})

	enabled, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("create enabled: %v", err)
	}
	disabled, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "cs", Kind: domain.AnnounceKindCrossSeedV6, BaseURL: "http://cs:2468", APIKey: "k", HarbrrURL: "http://h",
	})
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	if err := svc.SetEnabled(ctx, disabled.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	rel := announce.Release{Name: "X", GUID: "g1"}
	matched := svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release {
		return []announce.Release{rel}
	})

	if matched != 1 {
		t.Errorf("matched = %d, want 1 (only the enabled connection)", matched)
	}
	if got := targets[enabled.ID]; got == nil || len(got.got) != 1 {
		t.Errorf("enabled connection not pushed to: %+v", targets[enabled.ID])
	}
	if got := targets[disabled.ID]; got != nil {
		t.Error("disabled connection should not have a built target")
	}
}

// TestServiceUpdateConnection proves UpdateConnection is name-only now (identity/
// credential moved to the App): a non-nil Name patches, and BaseURL/HarbrrURL/
// APIKeyEncrypted — which UpdateConnectionParams no longer even exposes — stay exactly
// as CreateConnection left them.
func TestServiceUpdateConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "orig_key", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	origCipher := conn.APIKeyEncrypted

	if err := svc.UpdateConnection(ctx, conn.ID, announce.UpdateConnectionParams{Name: ptr("qui-renamed")}); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}
	got, err := svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.Name != "qui-renamed" {
		t.Errorf("name not applied: %+v", got)
	}
	if got.BaseURL != "http://qui:7476" || got.HarbrrURL != "http://h:8787" {
		t.Errorf("identity moved despite a name-only patch: %+v", got)
	}
	if got.APIKeyEncrypted != origCipher {
		t.Errorf("a name-only patch rotated the stored key: %q -> %q", origCipher, got.APIKeyEncrypted)
	}
}

// TestServiceUpdateValidation proves a partial edit can't persist a blank name, and
// that an unknown id surfaces as not-found.
func TestServiceUpdateValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })
	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if err := svc.UpdateConnection(ctx, conn.ID, announce.UpdateConnectionParams{Name: ptr("  ")}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("UpdateConnection(blank name) err = %v, want ErrInvalid", err)
	}

	if err := svc.UpdateConnection(ctx, 9999, announce.UpdateConnectionParams{Name: ptr("x")}); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("UpdateConnection(unknown id) err = %v, want ErrNotFound", err)
	}
}

// TestServiceTestConnection drives TestConnection through the real per-kind drivers
// (DefaultTargetFactory) against httptest servers: qui probes its webhook/check
// (2xx/404 pass, 500 fail) without ever calling apply; cross-seed v6 probes /api/ping
// (up pass, down fail). An unknown id is not-found.
func TestServiceTestConnection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		kind    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "qui reachable",
			kind: domain.AnnounceKindQui,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/cross-seed/apply" {
					t.Error("probe must never call apply")
				}
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			name:    "qui no-match 404 is reachable",
			kind:    domain.AnnounceKindQui,
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		},
		{
			name:    "qui 500 fails",
			kind:    domain.AnnounceKindQui,
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			wantErr: true,
		},
		{
			name: "csv6 ping up",
			kind: domain.AnnounceKindCrossSeedV6,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/ping" {
					t.Errorf("probe hit %q, want /api/ping", r.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			name:    "csv6 ping down",
			kind:    domain.AnnounceKindCrossSeedV6,
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			svc, _, _ := newService(t, announce.DefaultTargetFactory(srv.Client(), nil, nil))
			conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
				Name: tt.name, Kind: tt.kind, BaseURL: srv.URL, APIKey: "k", HarbrrURL: srv.URL,
			})
			if err != nil {
				t.Fatalf("CreateConnection: %v", err)
			}
			if err := svc.TestConnection(ctx, conn.ID); (err != nil) != tt.wantErr {
				t.Errorf("TestConnection err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })
	if err := svc.TestConnection(context.Background(), 9999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("TestConnection(unknown id) err = %v, want ErrNotFound", err)
	}
}

// TestServicePushSwallowsErrors proves a per-connection announce failure is logged, not
// propagated, and never blocks the rest of the fan-out.
func TestServicePushSwallowsErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return &fakeTarget{err: errors.New("boom")}, nil
	})
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	matched := svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release {
		return []announce.Release{{Name: "X", GUID: "g1"}}
	})
	if matched != 0 {
		t.Errorf("matched = %d, want 0 (the announce errored)", matched)
	}
}

// TestServicePushFailureRedactsGUID pins #230: the push-failure warn logs the release
// GUID, and for passkey-in-GUID trackers (FileList-style) the GUID is the
// credential-bearing download URL — it must log scrubbed, never in cleartext.
func TestServicePushFailureRedactsGUID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	var buf bytes.Buffer
	appsSvc := apps.NewService(db, kr, http.DefaultClient, zerolog.Nop())
	svc := announce.NewService(db, appsSvc, auth.NewService(db), kr, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return &fakeTarget{err: errors.New("boom")}, nil
	}, zerolog.New(&buf))
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	const secret = "SECRETPASSKEY123" //nolint:gosec // G101: synthetic test passkey
	svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release {
		return []announce.Release{{Name: "X", GUID: "https://tracker.example/download.php?id=1&passkey=" + secret}}
	})
	logged := buf.String()
	if !strings.Contains(logged, "push failed") {
		t.Fatalf("expected a push-failed warn, got %q", logged)
	}
	if strings.Contains(logged, secret) {
		t.Errorf("log leaks the passkey: %q", logged)
	}
}

// hangTarget hangs on ctx.Done() for exactly one call (simulating a stuck request against a
// dead-but-not-erroring target) and returns immediately for every other call.
type hangTarget struct {
	hangAt int
	calls  int
}

func (h *hangTarget) Announce(ctx context.Context, _ announce.Release) (announce.Result, error) {
	idx := h.calls
	h.calls++
	if idx == h.hangAt {
		<-ctx.Done()
		return announce.Result{}, ctx.Err()
	}
	return announce.Result{}, nil
}

func (h *hangTarget) Probe(context.Context) error { return nil }

// TestServicePushOneCapsPerReleaseTimeout pins #232: without a per-release deadline, one
// stuck release consumes the whole shared batch context and every release queued behind it
// in the loop starves too. Each release must get its own bounded window instead.
func TestServicePushOneCapsPerReleaseTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tgt := &hangTarget{hangAt: 0}
	svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return tgt, nil })
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	rels := []announce.Release{{Name: "stuck", GUID: "g0"}, {Name: "after1", GUID: "g1"}, {Name: "after2", GUID: "g2"}}
	start := time.Now()
	svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release { return rels })
	elapsed := time.Since(start)

	if tgt.calls != len(rels) {
		t.Fatalf("calls = %d, want %d (the stuck release must not starve the rest of the batch)", tgt.calls, len(rels))
	}
	if elapsed >= 2*announce.PerReleaseTimeout {
		t.Errorf("elapsed %v for %d releases (1 stuck), want close to PerReleaseTimeout (%v) — the stuck call must not compound", elapsed, len(rels), announce.PerReleaseTimeout)
	}
}

// TestServicePushBatchSummaryLogsOnce pins #232 point 3: a batch with several failures logs
// one summary line, not one WRN per failed release (94 identical lines was the log-spam
// complaint that buried the passkey leak in #230).
func TestServicePushBatchSummaryLogsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var buf bytes.Buffer
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	appsSvc := apps.NewService(db, kr, http.DefaultClient, zerolog.Nop())
	svc := announce.NewService(db, appsSvc, auth.NewService(db), kr, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return &fakeTarget{err: errors.New("boom")}, nil
	}, zerolog.New(&buf))
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	rels := make([]announce.Release, 20)
	for i := range rels {
		rels[i] = announce.Release{Name: "X", GUID: "g"}
	}
	svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release { return rels })

	if n := strings.Count(buf.String(), "push failed"); n != 1 {
		t.Errorf(`"push failed" appears %d times in the log, want exactly 1 (one batch summary, not one per release)`, n)
	}
}
