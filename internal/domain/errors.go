package domain

import "errors"

// ErrInvalid and ErrConflict are the shared input-mapping sentinels for harbrr's
// connection-resource services (appsync, announce, notify): a service wraps
// ErrInvalid for a rejected input (the handler turns it into 400) and ErrConflict
// for a unique-constraint violation (400 -> 409). Not-found flows through
// database.ErrNotFound instead, since "no such row" is not an input-mapping
// concern.
//
// These sentinels intentionally carry no service prefix (contrast
// registry.ErrInvalid, proxy.ErrInvalid, solver.ErrInvalid, which stay local to
// their packages and are out of scope for this move) — a caller wraps its own
// message context with fmt.Errorf("%w: ...", domain.ErrInvalid).
var (
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("already exists")
)
