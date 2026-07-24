package registry

import (
	"errors"
	"testing"
)

// TestNormalizePriority proves the Servarr priority contract: 0 defaults to
// defaultPriority, 1-50 pass through unchanged, and anything outside that range is
// rejected as ErrInvalid.
func TestNormalizePriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   int
		want    int
		wantErr bool
	}{
		{name: "zero defaults", input: 0, want: defaultPriority},
		{name: "minimum bound", input: 1, want: 1},
		{name: "maximum bound", input: 50, want: 50},
		{name: "mid-range passthrough", input: 25, want: 25},
		{name: "negative rejected", input: -1, wantErr: true},
		{name: "above max rejected", input: 51, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizePriority(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("normalizePriority(%d) err = %v, want ErrInvalid", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePriority(%d) unexpected err: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("normalizePriority(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestValidateMinSeeders proves the floor is non-negative-only.
func TestValidateMinSeeders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{name: "zero is unset, valid", input: 0},
		{name: "positive valid", input: 5},
		{name: "negative rejected", input: -1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMinSeeders(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("validateMinSeeders(%d) err = %v, want ErrInvalid", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateMinSeeders(%d) unexpected err: %v", tt.input, err)
			}
		})
	}
}

// TestResolvePriority proves the PATCH pointer semantics: nil keeps the instance's
// current (already-valid) priority untouched — no re-validation of a value that was
// never resubmitted — while a present pointer runs through normalizePriority.
func TestResolvePriority(t *testing.T) {
	t.Parallel()

	ten := 10
	invalid := 99
	tests := []struct {
		name    string
		update  *int
		current int
		want    int
		wantErr bool
	}{
		{name: "nil keeps current", update: nil, current: 30, want: 30},
		{name: "present zero defaults", update: new(int), current: 30, want: defaultPriority},
		{name: "present value sets", update: &ten, current: 30, want: 10},
		{name: "present invalid rejected", update: &invalid, current: 30, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolvePriority(tt.update, tt.current)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("resolvePriority err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePriority unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolvePriority = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestResolveMinSeeders mirrors TestResolvePriority for the min-seeders floor.
func TestResolveMinSeeders(t *testing.T) {
	t.Parallel()

	five := 5
	negative := -1
	tests := []struct {
		name    string
		update  *int
		current int
		want    int
		wantErr bool
	}{
		{name: "nil keeps current", update: nil, current: 7, want: 7},
		{name: "present value sets", update: &five, current: 7, want: 5},
		{name: "present negative rejected", update: &negative, current: 7, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveMinSeeders(tt.update, tt.current)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("resolveMinSeeders err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMinSeeders unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveMinSeeders = %d, want %d", got, tt.want)
			}
		})
	}
}
