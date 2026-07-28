package registry_test

import (
	"context"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// addWithExpiry adds the test indexer carrying an expiry triple.
func addWithExpiry(t *testing.T, reg *registry.Registry, p registry.AddParams) (domain.IndexerInstance, error) {
	t.Helper()
	p.Slug, p.DefinitionID = "tt", "testtracker"
	p.Settings = map[string]string{"apikey": "k", "sort": "seeders"}
	return reg.Add(context.Background(), p)
}

func TestAddNormalizesExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		in           registry.AddParams
		wantDate     string
		wantKind     string
		wantLifetime bool
		wantErr      bool
	}{
		{name: "unset stays unset", in: registry.AddParams{}},
		{
			name:     "a perk date round-trips",
			in:       registry.AddParams{ExpiresAt: "2027-01-31", ExpiryKind: domain.ExpiryKindPerk},
			wantDate: "2027-01-31", wantKind: domain.ExpiryKindPerk,
		},
		{
			name:     "surrounding whitespace is trimmed, not rejected",
			in:       registry.AddParams{ExpiresAt: " 2027-01-31 ", ExpiryKind: " account "},
			wantDate: "2027-01-31", wantKind: domain.ExpiryKindAccount,
		},
		{
			// Lifetime wins outright: leaving the date behind would let a later
			// un-ticking silently resurrect a stale expiry the operator had replaced.
			name:         "lifetime clears the date but keeps the kind",
			in:           registry.AddParams{ExpiresAt: "2027-01-31", ExpiryKind: domain.ExpiryKindPerk, ExpiryLifetime: true},
			wantKind:     domain.ExpiryKindPerk,
			wantLifetime: true,
		},
		{name: "a kind with no date is dropped with it", in: registry.AddParams{ExpiryKind: domain.ExpiryKindPerk}},
		{name: "a non-date is rejected", in: registry.AddParams{ExpiresAt: "next tuesday"}, wantErr: true},
		{name: "a non-existent day is rejected", in: registry.AddParams{ExpiresAt: "2027-02-31"}, wantErr: true},
		{name: "the wrong date order is rejected", in: registry.AddParams{ExpiresAt: "31-01-2027"}, wantErr: true},
		{name: "an unknown kind is rejected", in: registry.AddParams{ExpiresAt: "2027-01-31", ExpiryKind: "vip"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg, _ := newRegistry(t, nil)
			inst, err := addWithExpiry(t, reg, tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Add(%+v) succeeded, want an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add(%+v): %v", tt.in, err)
			}
			assertExpiry(t, inst, tt.wantDate, tt.wantKind, tt.wantLifetime)
			// Re-read: the point of the test is what is PERSISTED, not what Add returned.
			stored, _, err := reg.Get(context.Background(), "tt")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			assertExpiry(t, stored, tt.wantDate, tt.wantKind, tt.wantLifetime)
		})
	}
}

func TestUpdateExpiryPatchSemantics(t *testing.T) {
	t.Parallel()

	empty, later, account := "", "2028-06-30", domain.ExpiryKindAccount
	lifetimeOn, lifetimeOff := true, false

	tests := []struct {
		name         string
		patch        registry.UpdateParams
		wantDate     string
		wantKind     string
		wantLifetime bool
		wantErr      bool
	}{
		{
			name:     "an unrelated patch leaves the expiry alone",
			patch:    registry.UpdateParams{MinSeeders: intPtr(5)},
			wantDate: "2027-01-31", wantKind: domain.ExpiryKindPerk,
		},
		{
			name:     "renewing moves the date",
			patch:    registry.UpdateParams{ExpiresAt: &later},
			wantDate: "2028-06-30", wantKind: domain.ExpiryKindPerk,
		},
		{
			name:  "a present-but-empty date clears the tracking, kind included",
			patch: registry.UpdateParams{ExpiresAt: &empty},
		},
		{
			name:     "the kind can change on its own",
			patch:    registry.UpdateParams{ExpiryKind: &account},
			wantDate: "2027-01-31", wantKind: domain.ExpiryKindAccount,
		},
		{
			name:         "ticking lifetime clears the stored date",
			patch:        registry.UpdateParams{ExpiryLifetime: &lifetimeOn},
			wantKind:     domain.ExpiryKindPerk,
			wantLifetime: true,
		},
		{
			name:     "setting a date and un-ticking lifetime together works",
			patch:    registry.UpdateParams{ExpiresAt: &later, ExpiryLifetime: &lifetimeOff},
			wantDate: "2028-06-30", wantKind: domain.ExpiryKindPerk,
		},
		{
			name:    "a malformed date is rejected and changes nothing",
			patch:   registry.UpdateParams{ExpiresAt: strPtr("2027-13-01")},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg, _ := newRegistry(t, nil)
			if _, err := addWithExpiry(t, reg, registry.AddParams{
				ExpiresAt: "2027-01-31", ExpiryKind: domain.ExpiryKindPerk,
			}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			err := reg.Update(context.Background(), "tt", tt.patch)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Update succeeded, want an error")
				}
				tt.wantDate, tt.wantKind = "2027-01-31", domain.ExpiryKindPerk
			} else if err != nil {
				t.Fatalf("Update: %v", err)
			}
			stored, _, err := reg.Get(context.Background(), "tt")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			assertExpiry(t, stored, tt.wantDate, tt.wantKind, tt.wantLifetime)
		})
	}
}

func assertExpiry(t *testing.T, inst domain.IndexerInstance, date, kind string, lifetime bool) {
	t.Helper()
	if inst.ExpiresAt != date || inst.ExpiryKind != kind || inst.ExpiryLifetime != lifetime {
		t.Errorf("expiry = (%q, %q, %v), want (%q, %q, %v)",
			inst.ExpiresAt, inst.ExpiryKind, inst.ExpiryLifetime, date, kind, lifetime)
	}
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }
