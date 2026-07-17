package gazelle

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// authStrategy owns how one site authenticates ajax.php traffic and how its session
// lives (ADR 0003). Implementations are stateless per-call; any session state persists
// through the driver's setting store (d.persist) and session fields, not the strategy
// value itself. A site composes a strategy in sites.go — never a branch in a shared
// file — so adding a site is a table entry, not an auth.go/search.go/grab.go edit.
type authStrategy interface {
	// Prepare attaches credentials/session to an outgoing request, ensuring a session
	// exists first if the strategy needs one.
	Prepare(ctx context.Context, d *driver, req *stdhttp.Request) error
	// Recover handles an auth-classified failure (re-login, session rotation) and
	// reports whether the caller should retry once with a freshly prepared request.
	Recover(ctx context.Context, d *driver, cause error) (retry bool, err error)
	// Scrub returns the strategy's secret values for the Base.Scrub extras (parse.go's
	// scrubCredentials chokepoint), on top of the credentials Base.Scrub already
	// redacts from IsSecret settings.
	Scrub(d *driver) []string
}

// apiKeyAuth is Redacted/Orpheus: a static Authorization header built from the
// configured apikey setting, prefixed per site. Keys don't rotate server-side, so
// Recover never retries and Scrub has nothing to add beyond Base.Scrub's own
// IsSecret-derived coverage of the apikey setting.
type apiKeyAuth struct {
	prefix string
}

func (a apiKeyAuth) Prepare(_ context.Context, d *driver, req *stdhttp.Request) error {
	req.Header.Set("Authorization", a.prefix+d.Cfg["apikey"])
	return nil
}

func (apiKeyAuth) Recover(_ context.Context, _ *driver, cause error) (bool, error) {
	return false, cause
}

func (apiKeyAuth) Scrub(*driver) []string { return nil }

// generationError wraps an auth-classified error with the session generation that was
// in use when it occurred, threaded through Recover's cause parameter so formLoginAuth
// can coalesce concurrent renewals without widening the strategy interface: the
// generation travels as data inside the already-required error argument instead of a
// new method.
type generationError struct {
	error
	generation uint64
}

func (e generationError) Unwrap() error { return e.error }

func withGeneration(err error, generation uint64) error {
	return generationError{error: err, generation: generation}
}

func generationFrom(err error) uint64 {
	var wrapped generationError
	if errors.As(err, &wrapped) {
		return wrapped.generation
	}
	return 0
}

// sessionRejected is the exhausted-retry error: the strategy renewed once (or attempted
// to) and the retried attempt still failed authentication. op names the operation
// ("search" or "download") for the diagnostic.
func sessionRejected(op string) error {
	// Wraps login.ErrLoginFailed (not just its text) so errors.Is still matches and
	// the registry classifies the exhausted-renewal failure as an auth health event.
	return fmt.Errorf("gazelle: automatic session renewal did not authenticate %s; verify configured credentials: %w", op, login.ErrLoginFailed)
}

// sessionRetry runs attempt once, and on an auth-classified failure gives the site's
// strategy a single chance to recover (renew a session, no-op for apiKeyAuth) before
// retrying exactly once. It is the one place Search/Grab hand off to the strategy, so
// neither caller branches on which auth regime a site uses.
func sessionRetry[T any](ctx context.Context, d *driver, op string, attempt func(context.Context) (T, error)) (T, error) {
	var zero T
	result, attemptErr := attempt(ctx)
	if attemptErr == nil || !errors.Is(attemptErr, login.ErrLoginFailed) {
		return result, attemptErr
	}
	// attemptErr already carries the generation the request actually used (the closure
	// wraps it via withGeneration off newRequest's session), so Recover coalesces
	// against the right one — the pre-attempt snapshot would miss an in-attempt login.
	retry, recoverErr := d.site.strategy.Recover(ctx, d, attemptErr)
	if recoverErr != nil {
		return zero, recoverErr
	}
	if !retry {
		return zero, attemptErr
	}
	result, err := attempt(ctx)
	if err != nil && errors.Is(err, login.ErrLoginFailed) {
		return zero, sessionRejected(op)
	}
	return result, err
}
