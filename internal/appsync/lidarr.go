package appsync

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// NewLidarr builds a Target for a Lidarr instance. baseURL is the app's own origin
// (e.g. http://lidarr:8686); apiKey is its API key. Lidarr shares the Servarr fields[]
// contract but serves the indexer API at /api/v1; it has no anime categories.
func NewLidarr(baseURL, apiKey string, client *http.Client) Target {
	return newServarr(domain.AppKindLidarr, baseURL, apiKey, client, false, servarrIndexerPathV1)
}
