package torrentday

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// errNotImplemented marks the Grab/Test surface still to be filled in by later leaves
// (the /dl grab and the cookie probe). Search (request/parse) landed in this leaf.
var errNotImplemented = errors.New("torrentday: not implemented")

// Grab is a placeholder until the /dl grab leaf lands.
func (d *driver) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errNotImplemented
}

// Test is a placeholder until the cookie-probe leaf lands.
func (d *driver) Test(_ context.Context) error {
	return errNotImplemented
}
