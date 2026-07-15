package core

import (
	"context"
	"testing"
	"time"
)

func TestCacheInfoSinkRoundTrip(t *testing.T) {
	t.Parallel()
	feedClock := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)
	ctx, ci := WithCacheInfoSink(context.Background())
	RecordCacheInfo(ctx, CacheInfo{Cached: true, ExpiresAt: feedClock})
	if !ci.Cached || !ci.ExpiresAt.Equal(feedClock) {
		t.Fatalf("sink not filled: %+v", ci)
	}
	// Recording into a ctx without a sink must be a no-op (no panic).
	RecordCacheInfo(context.Background(), CacheInfo{Cached: true})
}
