// Package native hosts indexer drivers for families the declarative Cardigann
// format cannot express — currently only the AvistaZ network, whose login→Bearer
// `api/v1/jackett` auth exceeds the YAML format (so it has 0 vendored defs).
//
// A native driver satisfies Driver — the engine-shaped core the registry's adapter
// wraps, the same contract the Cardigann engine satisfies — so it flows through the
// existing registry caching, health recording, Torznab serializer, and /dl grab
// proxy unchanged. The adapter adds Info + health on top. Each family ships a
// Go-built, caps-only loader.Definition (never schema-validated — it has no
// login/search block) so mapper.Build, the credential store, and the addable-indexer
// list all work without a special case.
package native

import (
	"context"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Driver is the engine-shaped core the registry adapter wraps: the four methods
// both a native family driver and the Cardigann engine implement. The adapter adds
// Info() and health recording, so a Driver never needs to know about either.
type Driver interface {
	Capabilities() *mapper.Capabilities
	Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error)
	NeedsResolver() bool
	// DownloadNeedsAuth reports whether the download authenticates out-of-band (session
	// cookie / request header) and so must be routed through /dl rather than served
	// bare. A native driver whose NeedsResolver() is already true can return false.
	DownloadNeedsAuth() bool
	Grab(ctx context.Context, link string) (*search.GrabResult, error)
	// Test verifies the instance is usable (for a native driver: that the credentials
	// authenticate). The management "test indexer" action drives it.
	Test(ctx context.Context) error
}

// Params are the per-instance inputs the registry hands a native driver factory:
// the family's Go-built definition (id/name/type/links/caps/settings), the decrypted
// settings, the paced HTTP doer (the family's rate limit is on the definition's
// RequestDelay, so the doer is already paced), the resolved base URL, and the clock.
type Params struct {
	Def     *loader.Definition
	Cfg     map[string]string
	Doer    search.Doer
	BaseURL string
	Clock   func() time.Time
	// PersistSetting durably writes a (possibly rotated) setting value back to the
	// encrypted store for this instance, so a driver that refreshes a credential
	// mid-session survives a restart. Optional — nil for drivers that don't rotate
	// credentials; MyAnonamouse uses it to persist the rotated mam_id cookie.
	PersistSetting func(ctx context.Context, name, value string) error
}

// OffsetPager is the optional capability a native driver implements when it can forward
// offset/limit upstream for deep-set paging (currently only the generic Newznab driver).
// It is deliberately NOT a method on Driver: most drivers can't page upstream, and adding
// it to the core interface would force every driver, the engine wrapper, and the fakes to
// implement a method they'd answer false for. The registry adapter and the search-cache
// layer type-assert for it, so a driver that doesn't implement it is treated as non-paging.
type OffsetPager interface {
	SupportsOffsetPaging() bool
}

// Factory builds a Driver for one configured instance.
type Factory func(Params) (Driver, error)

// Family pairs a native family's Go-built definition with its driver factory. The
// registry resolves a configured instance's DefinitionID against the set of
// families before falling back to the Cardigann loader.
type Family struct {
	Definition *loader.Definition
	Factory    Factory
}
