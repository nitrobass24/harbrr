package registry

import (
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// diagnosticsRingSize is how many recent failed exchanges are retained per
// indexer. Small on purpose: this is diagnostic state for "what broke just now",
// not history — the durable health events already carry the timeline.
const diagnosticsRingSize = 5

// FailureCapture is one diagnostics ring entry: the health classification the
// registry assigned and when it happened, over the engine's already-redacted
// snapshot of the exchange (see search.Capture).
type FailureCapture struct {
	Kind       string
	OccurredAt time.Time
	Capture    search.Capture
}

// diagnostics is the registry-wide, MEMORY-ONLY store of recent failed fetches,
// keyed by instance id and newest-first. It is deliberately never persisted: a
// capture holds a (redacted) tracker response, so keeping it out of the database
// keeps it out of every backup, support bundle, and crash dump. A restart drops
// the lot, and so does deleting the instance (ForgetInstance).
//
// ponytail: one mutex over the whole map. Writes happen once per classified
// failure and reads once per diagnostics page view — per-instance locks would buy
// nothing measurable.
type diagnostics struct {
	mu   sync.Mutex
	byID map[int64][]FailureCapture
}

func newDiagnostics() *diagnostics {
	return &diagnostics{byID: map[int64][]FailureCapture{}}
}

// record prepends an entry for instanceID, evicting the oldest beyond
// diagnosticsRingSize.
func (d *diagnostics) record(instanceID int64, entry FailureCapture) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ring := append([]FailureCapture{entry}, d.byID[instanceID]...)
	if len(ring) > diagnosticsRingSize {
		ring = ring[:diagnosticsRingSize]
	}
	d.byID[instanceID] = ring
}

// list returns a copy of the instance's ring, newest-first; nil when nothing has
// been captured. The copy keeps a concurrent record from mutating a slice the
// caller is rendering.
func (d *diagnostics) list(instanceID int64) []FailureCapture {
	d.mu.Lock()
	defer d.mu.Unlock()
	ring := d.byID[instanceID]
	if len(ring) == 0 {
		return nil
	}
	return append([]FailureCapture(nil), ring...)
}

// ForgetInstance drops a deleted instance's captures, mirroring the other
// in-memory per-instance state the Manager purges after a committed Delete.
func (d *diagnostics) ForgetInstance(instanceID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.byID, instanceID)
}
