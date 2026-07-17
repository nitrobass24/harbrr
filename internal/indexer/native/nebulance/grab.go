package nebulance

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Grab fetches NBL's token-bearing download URL server-side and returns the torrent
// bytes. The link is already URL-credentialed (the apikey rides its query, as NBL
// hands it back from a search result), so it needs no extra auth header — the shared
// native.Base.GrabDirect covers this exactly (transport redaction, status
// classification, and the download size cap all live there).
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return d.GrabDirect(ctx, link, authClassify)
}
