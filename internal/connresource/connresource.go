// Package connresource extracts the lifecycle shared by harbrr's three
// encrypted-secret connection resources — appsync connections, announce
// connections, notification targets — into one generic module: an optional key
// mint with fail-closed orphan revoke plus insert-then-seal on create, a
// single-tx read/hook/patch/rotate/write on update, and a get/delete/fail-closed
// revoke on delete.
//
// This is composition, not an object-oriented port. Lifecycle[T] takes plain
// data and first-class functions (CreateSpec/UpdateSpec/DeleteSpec) — the same
// shape as http.Server or errgroup.Group — not an embedded base and not a
// Lifecycler interface: with one implementation an interface is not a seam. There
// is no reflection, no struct tags, no registry. Row operations stay in the
// existing repositories; Lifecycle only sequences them and owns the encryption
// and revoke steps.
//
// Hook tripwire: exactly one optional in-tx hook exists today — appsync's
// sync-profile reference check, on both create and update. If a second or third
// caller wants a hook, that is the signal to stop extending this package and
// redesign, not to grow it into a framework.
package connresource

import (
	"context"

	"github.com/autobrr/harbrr/internal/domain"
)

// KeyMinter mints a dedicated harbrr API key for a connection resource and
// revokes it later. appsync and announce mint one per connection so the remote
// side can call back into harbrr's feed; notify has nothing to mint, so its specs
// simply leave Minter nil. This is the single home for the interface appsync and
// announce each declared identically before this package existed.
type KeyMinter interface {
	MintAPIKey(ctx context.Context, name string) (string, domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id int64) error
}

// Secret is one encrypted-at-rest value bound to a resource: Discriminator is
// the AAD label distinguishing it from a resource's other secrets (e.g. "app",
// "harbrr", "url") and Plaintext is the value to seal.
type Secret struct {
	Discriminator string
	Plaintext     string
}
