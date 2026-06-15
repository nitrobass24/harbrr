// Package magnet reproduces Jackett's MagnetUtil: synthesising a public magnet
// from an info hash (and the reverse) byte-for-byte, so a harbrr-synthesised
// magnet matches Jackett's regardless of which stage builds it. It is a leaf
// package shared by the normalizer (post-search FixResults synthesis) and the
// download resolver (download.infohash → magnet), keeping a single source of
// truth for the construction.
package magnet

import (
	"net/url"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/encode"
)

// publicTrackers is the tracker list Jackett's MagnetUtil appends when
// synthesising a public magnet from an info hash, kept byte-for-byte so a
// harbrr-synthesised magnet matches Jackett's. Source:
// src/Jackett.Common/Utils/MagnetUtil.cs _Trackers.
var publicTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://vito-tracker.space:6969/announce",
	"udp://vito-tracker.duckdns.org:6969/announce",
	"udp://tracker.theoks.net:6969/announce",
	"udp://tracker.srv00.com:6969/announce",
	"udp://tracker.qu.ax:6969/announce",
	"udp://tracker.corpscorp.online:80/announce",
}

// FromInfoHash reproduces MagnetUtil.InfoHashToPublicMagnet: build
// magnet:?xt=urn:btih:<hash>&dn=<url-encoded title>&tr=...&tr=... . Jackett
// returns null (no magnet) when either the hash or the title is blank.
func FromInfoHash(infoHash, title string) string {
	if strings.TrimSpace(infoHash) == "" || strings.TrimSpace(title) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(infoHash)
	b.WriteString("&dn=")
	b.WriteString(urlEncode(title))
	for _, tr := range publicTrackers {
		b.WriteString("&tr=")
		b.WriteString(urlEncode(tr))
	}
	return b.String()
}

// ToInfoHash reproduces MagnetUtil.MagnetToInfoHash: read the xt query
// argument and return the segment after the final ':' (stripping the
// "urn:btih:" prefix). Case is preserved, matching Jackett. A magnet without a
// usable xt yields "".
func ToInfoHash(magnet string) string {
	xt := queryArg(magnet, "xt")
	if xt == "" {
		return ""
	}
	if i := strings.LastIndexByte(xt, ':'); i >= 0 {
		return xt[i+1:]
	}
	return xt
}

// queryArg extracts a single query-string argument from a magnet/URL string,
// mirroring ParseUtil.GetArgumentFromQueryString (split on the first '?', drop
// any '#' fragment, then parse). Returns "" when absent or unparseable.
func queryArg(raw, name string) string {
	_, qs, ok := strings.Cut(raw, "?")
	if !ok {
		return ""
	}
	qs, _, _ = strings.Cut(qs, "#") // drop any fragment
	values, err := url.ParseQuery(qs)
	if err != nil {
		return ""
	}
	return values.Get(name)
}

// urlEncode matches Jackett's MagnetUtil encoding of the magnet dn=/tr= values:
// WebUtilityHelpers.UrlEncode (= .NET WebUtility.UrlEncode, space -> '+'). A
// title containing ! * ( ) ~ encodes differently from Go's url.QueryEscape, so
// this routes through the encode package for exact parity.
func urlEncode(s string) string {
	return encode.WebUtilityEncode(s)
}
