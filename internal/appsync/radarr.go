package appsync

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/domain"
)

// NewRadarr builds a Target for a Radarr instance. baseURL is the app's own origin
// (e.g. http://radarr:7878); apiKey is its API key. Radarr shares Sonarr's Servarr v3
// Torznab contract but has no anime categories.
func NewRadarr(baseURL, apiKey string, client *http.Client) Target {
	return newServarr(domain.AppKindRadarr, baseURL, apiKey, client, false)
}
