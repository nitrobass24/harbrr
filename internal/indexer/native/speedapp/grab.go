package speedapp

import (
	"context"
	"errors"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

var errDownloadRequestFailed = errors.New("speedapp: download request failed")

// Grab fetches a secret-free release URL with the cached Bearer token, refreshing once
// on 401, and returns the torrent bytes through harbrr's authenticated /dl path.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, _, err := d.bearerGET(ctx, link, "", true)
	if err != nil {
		if resp != nil || errors.Is(err, login.ErrLoginFailed) || errors.Is(err, search.ErrParseError) {
			return nil, err
		}
		return nil, native.SanitizeGrabError(err, errDownloadRequestFailed)
	}
	return native.GrabResultFrom(resp), nil
}
