package native

// minSessionSecretLen is the shortest value SessionSecrets will treat as a session
// token. apphttp.ScrubValues is a literal longest-first strings.ReplaceAll with no
// length guard — deliberately so, it scrubs exactly what it is handed — which makes
// the length judgement the caller's. A serialized cookie header carries preference
// pairs alongside the session (keeplogged=1, lang=en), and handing "1" or "en" to a
// substring replace would rewrite every occurrence of those two characters in a
// captured body, shredding the artefact the capture exists to preserve. A Gazelle
// session token runs ~30 characters and the preference values are 1-2, so 12 sits
// with roughly 4x margin on both sides of the boundary.
const minSessionSecretLen = 12

// SessionSecrets keeps only the values long enough to be a session token, for a driver
// handing its live session values to Base.DoDownload's per-call captureSecrets. A
// session rotated at runtime is not a declared setting, so the definition-derived
// snapshot Base takes at construction structurally cannot cover it and a refused grab
// would persist it verbatim (autobrr/harbrr#508) — but the cookie header it comes from
// carries short preference values that must not reach a substring scrub (see
// minSessionSecretLen). Empty values are dropped by the same test, and nothing
// qualifying returns nil so the variadic spread at the call site passes nothing.
func SessionSecrets(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if len(v) >= minSessionSecretLen {
			out = append(out, v)
		}
	}
	return out
}
