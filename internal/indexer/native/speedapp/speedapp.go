// Package speedapp implements native drivers for trackers using the SpeedApp JSON API.
package speedapp

import (
	"context"
	"sync"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured SpeedApp tracker instance. Tokens are cached only for the
// life of this value; authMu protects token generation and the shared in-flight login.
type driver struct {
	native.Base

	authMu  sync.Mutex
	token   tokenVersion
	refresh *tokenRefresh
}

var _ native.Driver = (*driver)(nil)

// New builds one SpeedApp driver from decrypted instance settings.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("speedapp", p)
	if err != nil {
		return nil, err
	}
	return &driver{Base: b}, nil
}

// NeedsResolver is false because release download URLs contain no credential.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true because Grab adds the runtime Bearer token server-side.
func (d *driver) DownloadNeedsAuth() bool { return true }

// SupportsOffsetPaging reports that SpeedApp accepts page and itemsPerPage upstream.
func (d *driver) SupportsOffsetPaging() bool { return true }

// Test verifies the configured credentials by obtaining or reusing a token.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.tokenFor(ctx, nil)
	return err
}
