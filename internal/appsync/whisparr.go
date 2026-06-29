package appsync

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// NewWhisparr builds a Target for a Whisparr instance. baseURL is the app's own origin
// (e.g. http://whisparr:6969); apiKey is its API key. Whisparr is a Radarr v3 fork: it
// serves the indexer API at /api/v3 and has no anime categories.
func NewWhisparr(baseURL, apiKey string, client *http.Client) Target {
	return newServarr(domain.AppKindWhisparr, baseURL, apiKey, client, false, servarrIndexerPathV3)
}
