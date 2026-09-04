// Package grab owns harbrr's download seam: the one place a passkey-bearing
// download link is sealed into an opaque token, and the one place that token is
// opened, resolved, and fetched server-side. The invariant it exists to hold is
// "a passkey never leaves harbrr" — every surface that hands a release out
// (the Torznab feed's /dl proxy, the management JSON search, the announce
// background push to a cross-seed tool) mints its links here, and every surface
// that redeems one redeems it here.
//
// The seam is three pieces: the AEAD token (encodeDLToken/decodeDLToken, bound to
// the indexer id so a token cannot be replayed across indexers), the rewriters that
// swap a served link for a token-bearing URL (NewDLRewriter, NewManagementDLRewriter),
// and the redemption core (ResolveGrab, and ServeGrab for callers that have an
// http.ResponseWriter). The absolute-URL derivation the rewriters need — URLConfig
// and the /dl and /download base builders — lives here too, so the URL a link is
// sealed into and the route that opens it cannot drift apart.
//
// grab sits above internal/indexer/core (it serves a core.Indexer) and below any
// one transport: it knows HTTP as a serving detail but nothing of Torznab XML or
// the management JSON envelope, which its callers supply as an ErrorWriter.
// internal/web/torznabhttp (the feed) and internal/web/api (the management surface)
// both depend on grab; grab depends on neither. See docs/adr/0002.
package grab
