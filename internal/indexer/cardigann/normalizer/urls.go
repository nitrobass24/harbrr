package normalizer

import "github.com/autobrr/harbrr/internal/indexer/cardigann/internal/httpx"

// resolveURL reproduces Jackett's resolvePath: new Uri(base, path). An empty
// value stays empty. When there is nothing usable to resolve against — no base
// URL, or a base/ref that will not parse — the original value is returned, which
// keeps an already-absolute link intact.
func resolveURL(baseURL, ref string) string {
	if ref == "" {
		return ref
	}
	resolved, err := httpx.Resolve(baseURL, ref)
	if err != nil {
		return ref
	}
	return resolved
}
