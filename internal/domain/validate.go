package domain

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateAbsURL is the one canonical validator for harbrr's connection-resource
// URL fields (appsync's base/harbrr URLs, announce's base/harbrr URLs, notify's
// destination URL): it requires an absolute http(s) URL with a host, so a
// malformed or relative value is rejected at the boundary rather than persisted
// and later producing an unreachable connection or a host-less link. It returns
// the trimmed value; a caller that wants to persist the normalized form uses it,
// and a caller that intentionally persists its raw input (or already trims
// before calling) discards it.
func ValidateAbsURL(field, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%w: %s must be an absolute http(s) URL", ErrInvalid, field)
	}
	return trimmed, nil
}
