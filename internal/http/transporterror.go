package http

import (
	"errors"
	"fmt"
	"net/url"
)

// SafeTransportDetail renders a REDACTED, actionable cause from a *url.Error — the
// stdlib transport-failure shape (http.Client.Do always returns one) — for the paced
// client's trace log. It returns "<op> <scheme>://<host>: <redacted-cause>",
// deliberately dropping the path and query.
//
// Dropping the path/query is the load-bearing safety property: trackers hide the
// download secret in driver-specific places — a passkey in a query param, an
// rsskey/api_key in a PATH segment (beyond-hd's auto.<id>.<rsskey>, animebytes'
// /torrent/<id>/download/<passkey>) — and a generic query-only scrub (RedactURL)
// misses the path ones. Emitting only op + scheme://host is safe for every driver
// regardless of where it puts its secret; the host is enough to diagnose and is not a
// secret.
//
// It returns "" for a nil error OR any non-*url.Error. The caller MUST treat "" as
// "no safe detail — keep the fixed message": a non-*url.Error (an already-stringified
// error, an io read failure) may embed a path secret in free text that RedactError
// cannot scrub, so this never falls back to RedactError on an arbitrary error.
func SafeTransportDetail(err error) string {
	if err == nil {
		return ""
	}
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return ""
	}
	return fmt.Sprintf("%s %s: %s", uerr.Op, SchemeHost(uerr.URL), RedactError(uerr.Err))
}

// RedactURLError rebuilds any *url.Error in err's chain with host-only URL
// detail, so wrapping the result with %w cannot re-embed the raw URL:
// (*url.Error).Error() quotes its FULL URL into the message — including a parse
// failure's raw input — one layer below whatever redaction a wrap site applies
// to its own format args. Callers that wrap an error which may be a
// url.Parse/http-transport failure on a secret-bearing URL must route it
// through this first (the paced client's redactDoErr is the Do-path analogue).
// A non-*url.Error passes through unchanged.
func RedactURLError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	return fmt.Errorf("%s %s: %w", uerr.Op, SchemeHost(uerr.URL), uerr.Err)
}

// SchemeHost returns "<scheme>://<host>" for a raw URL, dropping the path, query, and
// userinfo. It is the safe way to surface "which endpoint" in a log or error without
// risking a path/query-embedded secret: RedactURL only scrubs secret query params by name
// and long hex/alnum PATH tokens by a length heuristic, so a native driver that hides a
// passkey/api_key/rsskey in a shorter or non-hex PATH segment (beyond-hd's
// auto.<id>.<rsskey>, animebytes' /download/<passkey>) would slip through RedactURL —
// but never through this, which emits no path at all. An unparseable or host-less URL
// yields the fixed placeholder rather than risk returning a secret-bearing fragment.
func SchemeHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return redactedValue
	}
	return u.Scheme + "://" + u.Host
}
