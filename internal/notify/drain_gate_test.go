package notify

import (
	"context"
	"fmt"
	"testing"
)

// TestDrainGateOrdersAddsBeforeWait pins the shutdown-window property: a dispatch
// racing an in-flight Drain either lands before the join or is dropped — never a
// WaitGroup Add during Wait (forbidden by sync.WaitGroup; under -race the ungated
// code trips the detector on this schedule). After Drain returns the gate reopens,
// so Drain stays usable as a flush/join between assertions (the suite's idiom).
func TestDrainGateOrdersAddsBeforeWait(t *testing.T) {
	t.Parallel()
	s, _ := newService(t)
	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for i := range 200 {
			// A distinct indexer per iteration: the health debounce keys on
			// (indexer, kind), so a fixed key would suppress everything after the
			// first event and the hammer would never actually reach the
			// Add-during-Wait window it exists to exercise (review finding).
			s.OnHealthEvent(context.Background(), fmt.Sprintf("idx-%d", i), "transport", "event racing drain")
		}
	}()
	for range 50 {
		s.Drain(context.Background())
	}
	<-stop
	s.Drain(context.Background()) // gate reopened: a final join still returns clean
}
