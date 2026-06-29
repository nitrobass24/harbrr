package appsync

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// NewReadarr builds a Target for a Readarr instance. baseURL is the app's own origin
// (e.g. http://readarr:8787); apiKey is its API key. Readarr (a book manager) shares the
// Servarr fields[] contract but serves the indexer API at /api/v1; no anime categories.
func NewReadarr(baseURL, apiKey string, client *http.Client) Target {
	return newServarr(domain.AppKindReadarr, baseURL, apiKey, client, false, servarrIndexerPathV1)
}
