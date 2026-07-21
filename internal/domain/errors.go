package domain

import "errors"

// ErrInvalid and ErrConflict are the shared input-mapping sentinels for harbrr's
// connection-resource services (appsync, announce, notify): a service wraps
// ErrInvalid for a rejected input (the handler turns it into 400) and ErrConflict
// for a unique-constraint violation (400 -> 409). Not-found flows through
// database.ErrNotFound instead, since "no such row" is not an input-mapping
// concern.
//
// These sentinels intentionally carry no service prefix — a caller wraps its own
// message context with fmt.Errorf("%w: ...", domain.ErrInvalid).
//
// registry.ErrInvalid/ErrConflict, proxy.ErrInvalid, solver.ErrInvalid, and
// backup.ErrInvalid/ErrConflict all wrap these (fmt.Errorf("<pkg>: %w", ...), or
// %.0w where a package's historical text diverges from the shared one), so
// errors.Is(err, domain.ErrInvalid) / errors.Is(err, domain.ErrConflict) is the
// only check the api layer needs regardless of which service produced the error.
var (
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("already exists")
)
