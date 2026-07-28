package core

import "errors"

// The self-imposed gate sentinels a serving surface must be able to tell apart from a
// tracker failure. They live here, on the Indexer seam, rather than inside the registry
// that raises them, because the distinction is part of what an Indexer's error means to
// its consumer: "harbrr chose not to ask" is a different fact from "the tracker broke",
// and the aggregate feed's status ledger (SearchAggregate) reports it as such.
var (
	// ErrBudgetExhausted marks a Search/Grab refused because the indexer's request
	// budget (autobrr/harbrr#251) has no capacity left for the current period — either
	// an operator-configured cap was reached, or a tracker's own quota error was
	// observed (the reactive-learning path). The registry's Search catches it to prefer
	// serving a stale cache entry; the breaker explicitly excludes it from tripping,
	// since a self-imposed budget guard is not a tracker failure worth suppressing
	// other consumers over.
	ErrBudgetExhausted = errors.New("registry: indexer request budget exhausted for this period")

	// ErrCircuitOpen is returned by the registry's dispatch gate in place of hitting the
	// tracker. It is deliberately outside the health classification: a skipped call is
	// not a new failure and must not feed back into the escalation it is itself
	// enforcing.
	ErrCircuitOpen = errors.New("registry: circuit open")
)
