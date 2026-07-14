# Torznab driver — divergence records

The native driver for the torrent-protocol twin of the Newznab API: a tracker that
exposes its own Torznab RSS/XML search endpoint (currently only the MoreThanTV
preset). MoreThanTV has no Cardigann definition; Jackett's `MoreThanTVAPI.cs` and
Prowlarr's generic `Torznab.cs` preset are the parity references. Indexed by
[`docs/divergences.md`](../../../../../docs/divergences.md) via the
`*/testdata/README.md` glob.

## `[Deliberate]` — non-XML body never echoed into the surfaced error

Jackett's `MoreThanTVAPI.PerformQuery` throws `new ExceptionWithConfigData(result
.ContentString, configData)` when a 2xx body does not start with `"<"` — the raw body
(which could echo the submitted apikey, e.g. an HTML "invalid key" page) becomes the
exception message. harbrr's `checkXMLBody` classifies the same condition as
`login.ErrLoginFailed` WITHOUT ever including body content in the error, honoring the
non-negotiable secret-redaction rule (AGENTS.md). Exercised by `search_test.go`.

## `[Deliberate]` — no fallback-to-search / no title `"+"` rewrite

Unlike the newznab sibling (which resets `t=` to `search` when a mode-specific search
carries no mode-specific id param, and rewrites a literal `"+"` in the query term to a
space before it is re-escaped), Jackett's `MoreThanTVAPI.PerformQuery` does neither: `t=`
is set directly from `query.IsTVSearch`/`IsMovieSearch` unconditionally, and `q` is sent
trimmed, as-is. This driver reproduces MoreThanTVAPI exactly rather than the newznab
sibling's richer request generator. Exercised by `request_test.go`.

## `[Deliberate]` — imdbid sent as-is, no `tt`-strip; season only when `>0`, no `"00"` quirk

The newznab sibling strips a leading `tt` from `imdbid` (bare-digits wire form) and
renders `season=0` as `"00"`. Jackett's MoreThanTVAPI does neither: `imdbid` is
`query.ImdbID` verbatim (already the canonical `"tt0000000"` form harbrr's
`search.Query.IMDBID` carries), and `season` is added only `if query.Season > 0`
(a zero/blank/negative season is omitted, not rewritten). Exercised by
`request_test.go`.

## `[Deliberate]` — apikey redaction-order + generic-entry scope match the newznab sibling

The apikey param is emitted last (a stability convention for redaction-safe golden
tests — Jackett's literal C# order interleaves it earlier; wire order is otherwise
irrelevant to a real server). There is deliberately no generic user-supplied-URL torznab
preset (mirroring newznab's `Family()`/`GenericDefinition()` shape): the driving issue
scoped that as in-scope only "if it falls out naturally", and with only one preset
(MoreThanTV) built so far there is no second site to prove the generic shape against —
tracked as a natural follow-up once AnimeTosho/Torrent Network (or another torznab
tracker) are added.

## `[Accepted]` — grabs/files read Jackett's plain-element fallback, not Prowlarr's attr-only form

Jackett's `BaseNewznabIndexer.ResultFromFeedItem` reads `size`/`files` from a
torznab:attr first, falling back to a plain `<size>`/`<files>` child element; `grabs` is
read ONLY from a plain `<grabs>` child element (no attr fallback at all). Prowlarr's
`TorznabRssParser` instead reads `grabs` from a torznab:attr. Since the driving issue
names Jackett's `ResultFromFeedItem` as the primary parser reference (cross-checked
against Prowlarr only "where Jackett is ambiguous" — it is not, here), and the real
MoreThanTV capture uses the plain `<grabs>` element form, this driver follows Jackett
literally. Exercised by `parse_test.go` (the real capture's `<grabs>2</grabs>`/
`<grabs>0</grabs>` elements).
