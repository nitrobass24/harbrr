package notify

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// TestUpdateNotificationNoLostUpdate pins that two overlapping UpdateNotification patches
// — one rotating the destination URL, one flipping OnHealthFailure — cannot lose each
// other's write. Each UpdateNotification is a full-row read-modify-write; without a
// transaction the two reads both see the pre-write row and the second commit reverts the
// first field (a rotated webhook URL silently reverting → sends keep going to the old,
// possibly decommissioned destination). With the RMW under one transaction (serialized by
// the single DB connection) the second writer reads the first's commit, so both the
// rotated URL and the new flag survive. Runs many interleavings under -race, asserting
// both fields landed each time. The URL is never printed (decrypt-and-compare only).
func TestUpdateNotificationNoLostUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, kr := newService(t)

	n, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeWebhook, URL: "https://old.example/hook",
		OnHealthFailure: ptrBool(true),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := range 40 {
		wantURL := fmt.Sprintf("https://new.example/hook?token=rotated-%d", i)
		// Alternate the flag each iteration so the flip is always a real change.
		wantHealth := i%2 == 0

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := svc.UpdateNotification(ctx, n.ID, UpdateNotificationParams{URL: &wantURL}); err != nil {
				t.Errorf("iter %d: rotate url: %v", i, err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := svc.UpdateNotification(ctx, n.ID, UpdateNotificationParams{OnHealthFailure: &wantHealth}); err != nil {
				t.Errorf("iter %d: flip on_health_failure: %v", i, err)
			}
		}()
		wg.Wait()

		got, err := svc.GetNotification(ctx, n.ID)
		if err != nil {
			t.Fatalf("iter %d: GetNotification: %v", i, err)
		}
		if got.OnHealthFailure != wantHealth {
			t.Fatalf("iter %d: on_health_failure = %v, want %v (flag write lost)", i, got.OnHealthFailure, wantHealth)
		}
		dec, err := kr.Decrypt(got.ID, secretURL, got.URLEncrypted)
		if err != nil {
			t.Fatalf("iter %d: decrypt url: %v", i, err)
		}
		if dec != wantURL {
			t.Fatalf("iter %d: url reverted (rotation lost)", i)
		}
	}
}
