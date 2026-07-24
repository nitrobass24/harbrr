package registry

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// cacheProbe is a test-only scaffold that drives SearchCache's cache-aside path over a
// fake core.Indexer, exactly as the flattened indexerAdapter.Search does in
// production — without needing a full native.Driver + adapter. It replaces the deleted
// cachedIndexer decorator and the sc.wrap helper for the cache-internal tests.
//
// It snapshots builtEpoch at construction (matching indexerAdapter's build-time capture in
// Registry.build), so the epoch timing the flightepoch/epoch_regression tests depend on is
// preserved. It implements the FULL core.Indexer — forwarding every method,
// including SupportsOffsetPaging, to inner — so the external handler tests can serve it.
type cacheProbe struct {
	inner      core.Indexer
	cache      *SearchCache
	instanceID int64
	cfg        map[string]string
	builtEpoch uint64
}

var _ core.Indexer = (*cacheProbe)(nil)

// probe builds a cacheProbe over inner, snapshotting the instance's invalidation epoch at
// construction — the same capture Registry.build performs into indexerAdapter.builtEpoch.
func (c *SearchCache) probe(inner core.Indexer, instanceID int64, cfg map[string]string) *cacheProbe {
	return &cacheProbe{inner: inner, cache: c, instanceID: instanceID, cfg: cfg, builtEpoch: c.instanceEpoch(instanceID)}
}

// Search mirrors indexerAdapter.Search's cache-aside stage: the runtime enabled toggle off
// runs the live search directly (no read or write-back); otherwise it routes through the
// cache over the inner fake's Search seam, keyed by the inner's paging capability.
func (p *cacheProbe) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	if !p.cache.tuning.Load().enabled {
		return p.cache.fetchLive(ctx, p.instanceID, p.inner.Search, q)
	}
	return p.cache.search(ctx, p.instanceID, p.cfg, p.builtEpoch, p.inner.Search, p.inner.SupportsOffsetPaging(), q)
}

func (p *cacheProbe) Info() core.IndexerInfo             { return p.inner.Info() }
func (p *cacheProbe) Capabilities() *mapper.Capabilities { return p.inner.Capabilities() }
func (p *cacheProbe) NeedsResolver() bool                { return p.inner.NeedsResolver() }
func (p *cacheProbe) DownloadNeedsAuth() bool            { return p.inner.DownloadNeedsAuth() }
func (p *cacheProbe) SupportsOffsetPaging() bool         { return p.inner.SupportsOffsetPaging() }

func (p *cacheProbe) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return p.inner.Grab(ctx, link) //nolint:wrapcheck // fake-inner passthrough; nothing to add.
}
