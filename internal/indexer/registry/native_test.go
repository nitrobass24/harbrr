package registry_test

import (
	"context"
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// TestNativeFamilyDispatch proves a configured AvistaZ-family instance is accepted
// by Add (validated against the native catalog, not the Cardigann loader), builds
// the native driver, and resolves through the registry as a torznab.Indexer — with
// the four families listed as addable definitions.
func TestNativeFamilyDispatch(t *testing.T) {
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "az",
		DefinitionID: "avistaz",
		Settings:     map[string]string{"username": "u", "password": "p", "pid": "x"},
	}); err != nil {
		t.Fatalf("Add(avistaz): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "az")
	if !ok {
		t.Fatal("native indexer should resolve")
	}
	if !idx.NeedsResolver() {
		t.Error("avistaz NeedsResolver = false, want true (downloads need the Bearer header)")
	}
	if idx.Capabilities().Modes["search"] == nil {
		t.Error("native caps missing the search mode")
	}
	if got := idx.Info().ID; got != "az" {
		t.Errorf("Info().ID = %q, want az", got)
	}

	if n := len(reg.NativeDefinitions()); n != 4 {
		t.Errorf("NativeDefinitions = %d, want 4 (avistaz/cinemaz/privatehd/exoticaz)", n)
	}
}

// TestNativeUnknownDefinitionRejected confirms a non-existent definition id is still
// rejected (the native branch does not loosen validation).
func TestNativeUnknownDefinitionRejected(t *testing.T) {
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	if _, err := reg.Add(context.Background(), registry.AddParams{Slug: "x", DefinitionID: "not-a-tracker"}); err == nil {
		t.Fatal("Add with an unknown definition id should fail")
	}
}

// TestNativeInstanceUpdatable confirms a native instance can be edited — Update must
// resolve its definition via the native catalog, not the Cardigann loader (which
// has no avistaz def). Renaming, changing the base URL, and rotating the pid must
// all succeed.
func TestNativeInstanceUpdatable(t *testing.T) {
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "az", DefinitionID: "avistaz",
		Settings: map[string]string{"username": "u", "password": "p", "pid": "old"},
	}); err != nil {
		t.Fatalf("Add(avistaz): %v", err)
	}
	name := "My AvistaZ"
	base := "https://avistaz.example/"
	if err := reg.Update(ctx, "az", registry.UpdateParams{
		Name: &name, BaseURL: &base, Settings: map[string]string{"pid": "rotated"},
	}); err != nil {
		t.Fatalf("Update(avistaz): %v", err)
	}
	inst, _, err := reg.Get(ctx, "az")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if inst.Name != name || inst.BaseURL != base {
		t.Errorf("after update: name=%q baseURL=%q, want %q / %q", inst.Name, inst.BaseURL, name, base)
	}
}
