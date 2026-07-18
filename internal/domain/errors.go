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

// ErrAppMigrationPending is returned by a surface service when a non-hostless row
// still has a NULL app_id — its identity/credential has not yet been folded into an
// App (the boot fold in internal/resourcemigrate failed and will retry next boot).
// The handler maps it to 503: the resource exists but cannot be used until the fold
// completes. It lives here (a leaf package) so the surface services and the handler
// error-mapping share the one sentinel.
var ErrAppMigrationPending = errors.New("app migration pending — restart harbrr or check logs")
