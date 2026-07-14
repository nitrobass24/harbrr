# Torznab driver — divergence records

The native driver for the torrent-protocol twin of the Newznab API: a tracker that
exposes its own Torznab RSS/XML search endpoint. The family carries a generic
user-supplied-URL entry plus the presets Prowlarr's `Torznab.cs` ships as
DefaultDefinitions (MoreThanTV, AnimeTosho, Torrent Network). None has a Cardigann
definition; Jackett's `MoreThanTVAPI.cs` and Prowlarr's `Torznab.cs` /
`TorznabRssParser.cs` are the parity references. Indexed by
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

## `[Deliberate]` — apikey emitted last, and omitted entirely when empty

The apikey param is emitted last (a stability convention for redaction-safe golden
tests — Jackett's literal C# order interleaves it earlier; wire order is otherwise
irrelevant to a real server) and is wholly absent when no key is configured: AnimeTosho
is a keyless public feed (its `keyNone` policy even drops a stray configured value so
it can never ride a request), and the generic entry's key is optional. Exercised by
`request_test.go`.

## `[Deliberate]` — apikey validation is per-preset, not a family rule

The 32-char length check is Jackett's **MoreThanTV-specific** add-time validation
(`MoreThanTVAPI.ApplyConfiguration`: "Expected length: 32") — Prowlarr's
`TorznabSettingsValidator` validates nothing for these sites (its `ApiKeyAllowList` is
empty). So each preset carries its own `keyPolicy`: MoreThanTV requires exactly 32
chars; Torrent Network requires a non-empty key of any length (its length is
undocumented); AnimeTosho has no key setting at all (public feed); the generic entry's
key is optional and unvalidated (an unknown server may or may not require one).
Exercised by `torznab_test.go` (`TestNewValidatesAPIKeyPerPolicy`).

## `[Deliberate]` — NeedsResolver is per-preset, evidence-driven, sealed by default

MoreThanTV: **true** — the real capture's `<link>`/enclosure both embed
`authkey`+`torrent_pass`. AnimeTosho: **false** — the real capture
(`torznab_animetosho.xml`) serves plain uncredentialed storage URLs
(`…/torrents/<id>.torrent`) plus public magnets, so there is nothing to seal.
Torrent Network and the generic entry: **true** — their link shapes are unknown, and
sealing is the safe default (over-sealing costs a proxy hop; leaking a credentialed
URL to the *arr is a secret exposure). Exercised by `torznab_test.go`
(`TestFamilies`).

## `[Accepted]` — the generic entry advertises only search/tv-search/movie-search

Prowlarr's generic Torznab derives its caps (including music/book search) from a live
`?t=caps` fetch harbrr's torznab driver does not perform, and the driver's request
generator (Jackett's MoreThanTVAPI mapping) has no artist/album/author params.
Advertising a mode whose params would be silently dropped is worse than clean
degradation, so the placeholder caps advertise exactly the three modes the request
generator expresses. Revisit if a torznab site needs music/book search (which likely
also means adopting the newznab sibling's live caps fetch).

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
