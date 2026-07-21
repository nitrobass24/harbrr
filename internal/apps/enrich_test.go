package apps_test

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

// TestBind exercises the Get+DecryptKey pairing driver-build needs: a seeded app
// returns its App plus the round-tripped plaintext credential; an unknown id passes
// through database.ErrNotFound (from Get); a corrupted ciphertext fails at the
// decrypt step (from DecryptKey) — both without leaking the credential in the error.
func TestBind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name string
		// setup seeds whatever Bind's id needs and returns the id to Bind.
		setup     func(t *testing.T, svc *apps.Service, db *database.DB) int64
		wantErrIs error
		wantErr   bool
		wantKey   string
	}{
		{
			name: "seeded app round-trips its credential",
			setup: func(t *testing.T, svc *apps.Service, _ *database.DB) int64 {
				t.Helper()
				app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "secret-1"})
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				return app.ID
			},
			wantKey: "secret-1",
		},
		{
			name:      "unknown id",
			setup:     func(*testing.T, *apps.Service, *database.DB) int64 { return 999999 },
			wantErr:   true,
			wantErrIs: database.ErrNotFound,
		},
		{
			name: "corrupted ciphertext fails to decrypt",
			setup: func(t *testing.T, svc *apps.Service, db *database.DB) int64 {
				t.Helper()
				app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", APIKey: "k"})
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				// Not valid base64 (the "-" isn't in the standard alphabet), so DecryptKey
				// fails at the decode step — cheap to construct, no need to forge a
				// wrong-AAD ciphertext.
				if _, err := db.ExecContext(ctx, "UPDATE apps SET api_key_encrypted = 'not-valid-ciphertext' WHERE id = ?", app.ID); err != nil {
					t.Fatalf("corrupt ciphertext: %v", err)
				}
				return app.ID
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, db := newService(t)
			id := tt.setup(t, svc, db)

			app, key, err := svc.Bind(ctx, id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Bind(%d) err = nil, want an error", id)
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Errorf("Bind(%d) err = %v, want errors.Is %v", id, err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Bind(%d): %v", id, err)
			}
			if app.ID != id {
				t.Errorf("Bind(%d) app.ID = %d, want %d", id, app.ID, id)
			}
			if key != tt.wantKey {
				t.Errorf("Bind(%d) key = %q, want %q", id, key, tt.wantKey)
			}
		})
	}
}

// enrichRow is a minimal local row type for exercising EnrichList/EnrichOne without
// pulling in a real surface's domain type.
type enrichRow struct {
	AppID   *int64
	BaseURL string
}

func enrichRowAppID(r *enrichRow) *int64 { return r.AppID }

func enrichRowApply(r *enrichRow, a domain.App) { r.BaseURL = a.BaseURL }

// TestEnrichList proves the list-tolerant contract in one table: a nil app id and a
// dangling (unknown) app id both keep the row's stored field untouched, while a valid
// id gets overwritten from the one Index lookup.
func TestEnrichList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	dangling := int64(999999)

	rows := []enrichRow{
		{AppID: nil, BaseURL: "stored-nil"},
		{AppID: &dangling, BaseURL: "stored-dangling"},
		{AppID: &app.ID, BaseURL: "stored-valid"},
	}

	if err := apps.EnrichList(ctx, svc, rows, enrichRowAppID, enrichRowApply); err != nil {
		t.Fatalf("EnrichList: %v", err)
	}

	if rows[0].BaseURL != "stored-nil" {
		t.Errorf("nil-id row = %q, want stored field kept (stored-nil)", rows[0].BaseURL)
	}
	if rows[1].BaseURL != "stored-dangling" {
		t.Errorf("dangling-id row = %q, want stored field kept (stored-dangling)", rows[1].BaseURL)
	}
	if rows[2].BaseURL != app.BaseURL {
		t.Errorf("valid-id row = %q, want the App's BaseURL %q", rows[2].BaseURL, app.BaseURL)
	}
}

// TestEnrichOne proves the get-strict contract: a nil app id is a no-op (row
// untouched, nil error), a valid id applies the App's fields, and a dangling id
// errors instead of silently skipping (the asymmetry with EnrichList).
func TestEnrichOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	dangling := int64(999999)

	tests := []struct {
		name    string
		row     enrichRow
		wantErr bool
		want    string
	}{
		{name: "nil id is a no-op", row: enrichRow{AppID: nil, BaseURL: "stored"}, want: "stored"},
		{name: "valid id applies", row: enrichRow{AppID: &app.ID, BaseURL: "stored"}, want: app.BaseURL},
		{name: "dangling id errors", row: enrichRow{AppID: &dangling, BaseURL: "stored"}, wantErr: true, want: "stored"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			row := tt.row
			err := apps.EnrichOne(ctx, svc, &row, enrichRowAppID, enrichRowApply)
			if tt.wantErr && err == nil {
				t.Fatal("EnrichOne err = nil, want an error (dangling app id)")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("EnrichOne: %v", err)
			}
			if row.BaseURL != tt.want {
				t.Errorf("row.BaseURL = %q, want %q", row.BaseURL, tt.want)
			}
		})
	}
}
