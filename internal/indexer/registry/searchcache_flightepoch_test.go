package registry

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestServeMissDoesNotCoalesceAcrossEpochs proves cacheFlightKey's whole point: a
// singleflight follower whose OWN builtEpoch has advanced past an in-flight leader's
// must never coalesce onto (and receive) that leader's stale-epoch result — it drives
// its own independent live search instead.
//
// A leader (builtEpoch 0) starts a gated live search on a fresh (empty) cache key, so
// it registers a real singleflight flight and stays in it. While it is still blocked,
// the instance epoch is bumped (no purge — this isolates the flight-coalescing fix
// from the already-covered storeBestEffort write-skip, U8R-F4). A follower wrapped
// AFTER the bump (builtEpoch 1) searches the SAME cache key. Before the fix, both
// shared the plain cache key as the singleflight key, so the follower would coalesce
// onto the leader's still-blocked flight and either hang until the leader's gate
// released or receive the leader's stale-epoch release set. After the fix (the flight
// key includes builtEpoch), the follower drives its own live search and returns
// immediately with its own result, never touching the leader's gate.
func TestServeMissDoesNotCoalesceAcrossEpochs(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	ctx := context.Background()
	q := search.Query{Keywords: "epoch-flight"}

	leaderGate := make(chan struct{})
	leaderInner := &fakeInner{releases: relSet("leader1", "leader2"), gate: leaderGate, firstSeen: make(chan struct{})}
	leaderIdx := sc.probe(leaderInner, instID, nil) // captures builtEpoch = 0

	leaderDone := make(chan []*normalizer.Release, 1)
	go func() {
		got, err := leaderIdx.Search(ctx, q)
		if err != nil {
			t.Errorf("leader search: %v", err)
			leaderDone <- nil
			return
		}
		leaderDone <- got
	}()

	select {
	case <-leaderInner.firstSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("leader search never started")
	}

	// Advance the instance epoch WITHOUT purging: isolates the flight-coalescing fix
	// under test from the DB write-skip behavior a different test already covers.
	sc.bumpInstanceEpoch(instID)

	followerInner := &fakeInner{releases: relSet("follower1", "follower2")}
	followerIdx := sc.probe(followerInner, instID, nil) // captures builtEpoch = 1

	followerDone := make(chan []*normalizer.Release, 1)
	go func() {
		got, err := followerIdx.Search(ctx, q)
		if err != nil {
			t.Errorf("follower search: %v", err)
			followerDone <- nil
			return
		}
		followerDone <- got
	}()

	// The follower must complete on its OWN, without waiting for the leader's gate: if
	// it coalesced onto the leader's still-blocked flight, this would time out.
	var followerResult []*normalizer.Release
	select {
	case followerResult = <-followerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("follower search did not complete independently — it coalesced onto the stale-epoch leader's in-flight singleflight call")
	}
	if len(followerResult) != 2 || followerResult[0].Title != "follower1" {
		t.Fatalf("follower result = %+v, want its own fresh release set (leader's, not coalesced)", followerResult)
	}
	if got := followerInner.callCount(); got != 1 {
		t.Fatalf("follower inner Search calls = %d, want 1 (drove its own live search)", got)
	}

	// Release the leader; it must still complete with its OWN result — the fix must
	// not disrupt the leader's own flight.
	close(leaderGate)
	select {
	case leaderResult := <-leaderDone:
		if len(leaderResult) != 2 || leaderResult[0].Title != "leader1" {
			t.Fatalf("leader result = %+v, want its own release set", leaderResult)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leader search never completed after its gate released")
	}
}
