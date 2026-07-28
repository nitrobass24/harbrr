package notify

import (
	"context"
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
		for i := 0; i < 200; i++ {
			s.OnHealthEvent(context.Background(), "idx", "transport", "event racing drain")
		}
	}()
	for i := 0; i < 50; i++ {
		s.Drain(context.Background())
	}
	<-stop
	s.Drain(context.Background()) // gate reopened: a final join still returns clean
}
