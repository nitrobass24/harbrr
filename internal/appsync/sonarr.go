package appsync

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// NewSonarr builds a Target for a Sonarr instance. baseURL is the app's own origin
// (e.g. http://sonarr:8989); apiKey is its API key. Sonarr carries anime categories.
func NewSonarr(baseURL, apiKey string, client *http.Client) Target {
	return newServarr(domain.AppKindSonarr, baseURL, apiKey, client, true, servarrIndexerPathV3)
}
