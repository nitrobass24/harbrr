package normalizer

import "net/url"

// resolveURL reproduces Jackett's resolvePath: new Uri(base, path). An absolute
// path returns unchanged; a relative path resolves against baseURL. When baseURL
// is empty or unparseable the original value is returned (no base to resolve
// against), which keeps already-absolute links intact.
func resolveURL(baseURL, ref string) string {
	if ref == "" {
		return ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if refURL.IsAbs() {
		return refURL.String()
	}
	if baseURL == "" {
		return ref
	}
	base, err := url.Parse(baseURL)
	if err != nil || !base.IsAbs() {
		return ref
	}
	return base.ResolveReference(refURL).String()
}
