// Package http is the shared HTTP support layer: auth/session, cookie jar, the
// pluggable FlareSolverr solver interface, and secret redaction.
//
// No unredacted secret ever reaches a log line. The name-matched redaction
// helpers (RedactURL, RedactURLIdentity, RedactError, HostAndRedactedQuery)
// are the single chokepoint every log/error/trace site routes URLs and error
// messages through, so passkeys, API keys, and download tokens never leak.
// Raw request/response HEADERS (Cookie/Set-Cookie/Authorization) and solver
// JSON BODIES are covered by a stricter rule: they must not be logged at all —
// there is no header/body redaction helper, and any change that starts logging
// them must reintroduce one, with redaction tests, in the same PR (see
// docs/security.md). ScrubValues (scrub.go) is the separate VALUE-based
// sanitization seam alongside the name-matched scrubs: it erases known
// credential values echoed back in free text. The helpers live here (rather
// than in a single engine stage) because the login, search, and download
// stages all reuse them.
//
// See AGENTS.md and docs/architecture.md.
package http
