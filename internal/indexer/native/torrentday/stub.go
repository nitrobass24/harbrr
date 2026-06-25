package torrentday

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// errNotImplemented marks the Search/Grab/Test surface still to be filled in by later
// leaves (request/parse, the /dl grab, and the cookie probe). Leaf 1 is the
// sites/caps/settings + driver skeleton only.
var errNotImplemented = errors.New("torrentday: not implemented")

// Search is a placeholder until the JSON request/parse leaf lands.
func (d *driver) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	return nil, errNotImplemented
}

// Grab is a placeholder until the /dl grab leaf lands.
func (d *driver) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errNotImplemented
}

// Test is a placeholder until the cookie-probe leaf lands.
func (d *driver) Test(_ context.Context) error {
	return errNotImplemented
}
