# BroadcastTheNet native driver — fixtures & divergences

This is the **Native indexers** layer record indexed by [`docs/divergences.md`](../../../../../docs/divergences.md).
It pins where harbrr's BroadcastTheNet native driver **knowingly differs** from the
Prowlarr source it reproduces (`Prowlarr/Prowlarr` `develop`:
`BroadcastheNetRequestGenerator`, `BroadcastheNetParser`, `BroadcastheNetTorrent`,
`BroadcastheNetSettings`, `BroadcastheNet.cs` — note Prowlarr's "BroadcastheNet" typo).
The disposition vocabulary (`[Deliberate]` / `[Accepted]` / `[Tracked]`) is defined in
`docs/divergences.md`.

The goldens here are **synthetic** — derived from Prowlarr's documented contract and
autobrr's `pkg/btn` golden responses, never captured from a live BTN. A live search/grab
is the **live-validation** gate. All synthetic secrets (the `authkey`/`torrent_pass` in
the `DownloadURL`s) exist only to prove redaction and live only in `testdata/**` and
`*_test.go`.

## Fixtures

- `getTorrents_response.json` — a three-torrent JSON-RPC success body: a 1080p WEB-DL
  episode (internal), a 2160p Blu-ray season pack, and an SD HDTV episode (scene). The
  three torrents are keyed in **non-sorted** insertion order (`1555073`, `1555200`,
  `1555000`) so the deterministic sort-by-`TorrentID` is exercised. Every field is a JSON
  string (BTN's wire shape). Pins the base `btnTorrent`→`normalizer.Release` mapping, the
  Resolution-keyed category (`1080p`→TV/HD 5040, `2160p`→TV/UHD 5045, `SD`→TV/SD 5030),
  `Peers=Seeders+Leechers`, `Grabs=Snatched`, the unix `Time`→UTC publish date, and
  `TVDBID`/`RageID` (`parse_test.go`).
- `empty.json` — a `{"result":{"results":"0","torrents":{}}}` body → zero releases.
- `empty_array.json` — a zero-result body whose `torrents` field is a JSON **array**
  (`[]`) rather than an object, the shape PHP emits for an empty associative array → zero
  releases, no error (a straight map decode would otherwise fail).
- `bad_key.json` — a `{"result":null,"error":{"code":-32001,"message":"Invalid API Key"}}`
  body → mapped to `login.ErrLoginFailed`.

## Parse divergences (`BroadcastheNetParser`)

- **Category derived from `Resolution`, single newznab id** — `[Deliberate]`. BTN's
  newznab category is not a tracker category id; the parser maps the torrent's
  `Resolution` string through the Resolution-keyed caps map (`SD`/`Portable Device`→
  TV/SD 5030, `720p`/`1080p`/`1080i`→TV/HD 5040, `2160p`→TV/UHD 5045) and falls back to
  the TV root (5000) for an unmapped/blank resolution — exactly one category per release,
  matching Prowlarr `SetCapabilities`. harbrr's caps mapper additionally synthesises a
  1:1 custom category (an id ≥ 100000) for each mapping; the parser **discards** that
  synthetic id and keeps only the canonical newznab category, so the feed is not polluted
  with a per-resolution custom category.
- **All fields decoded tolerantly from JSON strings** — `[Accepted]`. BTN wire-encodes
  every field as a JSON string, including numerics (`TorrentID`, `Size`, `Time`,
  `Seeders`, `Leechers`, `Snatched`, `TvdbID`, `TvrageID`). `flexString` decodes a string
  OR a bare number, and a malformed numeric degrades to 0 rather than failing the page
  (autobrr's `pkg/btn` confirms the all-strings shape).
- **Deterministic sort by `TorrentID`** — `[Deliberate]`. The `torrents` object is a map
  keyed by id and iterates in an unspecified order, so releases are sorted by numeric
  `TorrentID` ascending before returning, for a stable feed and stable tests. Prowlarr
  preserves the server's (also unspecified) map order.
- **`Title=ReleaseName` verbatim** — `[Tracked]`. The parser sets `Title` to the raw
  `ReleaseName`. Prowlarr additionally strips backslashes and, for M2TS/ISO containers,
  rewrites `H.265`→`HEVC` / `H.264`→`AVC`. harbrr does not yet reproduce those rewrites;
  none of the fixtures exercise them. Revisit if a live diff shows a title mismatch.
- **`Origin` flags (Internal/Scene) dropped** — `[Accepted]`. `normalizer.Release` has no
  indexer-flags field, so Prowlarr's `Internal`/`Scene` flags (from `Origin`) are not
  carried. Nothing in harbrr's Torznab feed consumes them.
- **`Guid`/`InfoUrl` omitted** — `[Accepted]`. Prowlarr sets `Guid="BTN-{TorrentID}"` and
  an `InfoUrl` to the torrents.php details page. harbrr's `Link` is the `DownloadURL`
  (routed through `/dl`); the details URL is not yet emitted. Revisit if a details link
  is wanted.
- **Volume factors fixed at 1/1** — `[Accepted]`. BTN exposes no freeleech signal in the
  getTorrents response, so `DownloadVolumeFactor`/`UploadVolumeFactor` are 1.
- **Error envelope handling** — `[Accepted]`. A `-32001` ("Invalid API Key") error maps
  to `login.ErrLoginFailed`; any other JSON-RPC error or a `null` result is a parse error
  (with the apikey scrubbed from the message). A malformed body is a parse error. The
  HTTP-status auth/rate-limit handling lives in `search.go` (a later leaf).

## Request divergences (`BroadcastheNetRequestGenerator`)

- **Season-only query fetches the SEASON pack arm** — `[Deliberate]`. Prowlarr fans a
  season-only query out across two requests: an Episode arm (`Category "Episode"` /
  `Name "S{NN}E%"`) and a Season arm (`Category "Season"` / `Name "Season N%"`). BTN files
  season packs under the **Season** arm — the Episode `S01E%` query never matches them.
  In harbrr's single-page model (one request, like FileList) the season-only query emits
  the **Season** arm (`Category "Season"`, `Name "Season N%"`) so season packs are found;
  the standard episode arm (`Category "Episode"`, `Name "S{NN}E{EE}%"`) is unchanged for
  season+episode queries.
- **`Call Limit Exceeded` HTTP 200 body → rate limit** — `[Accepted]`. BTN signals its API
  rate limit as an HTTP 200 body containing `Call Limit Exceeded` (not a 429). `search.go`
  detects that marker (case-insensitive) before parsing and returns a
  `*search.RateLimitedError`, matching Prowlarr's `RequestLimitReachedException` (thrown
  before JSON parse). The marker is only checked on the JSON-RPC search response, not the
  binary `/dl` torrent fetch (a `.torrent` body is opaque bytes and BTN does not emit the
  marker there).
- **Absolute-episode query is a no-op** — `[Deliberate]`. A bare non-negative-integer
  keyword paired with a TVDB/TVRage id and no season/episode/daily is an absolute-episode
  lookup BTN cannot serve (it indexes by season/episode `Name`, not absolute number).
  Prowlarr returns an empty request chain for this shape; harbrr returns zero releases
  **without issuing the POST**.

## Live validation

- **Live search / grab / differential** — `[Tracked]`. Not yet run; the synthetic
  goldens are the offline gate. The live BTN search and grab (through a running harbrr
  container) are the live-validation gate, to be recorded here when run.
- **Download-URL credential redaction** — `[Accepted]`. The `DownloadURL` embeds
  `authkey`/`torrent_pass` in its **query**, exactly `RedactURL`'s scope; the URL reaches
  only the `/dl` proxy (`NeedsResolver()=true`), never the served feed, and the driver
  keeps it out of every error it raises. `scrubAPIKey` additionally strips the apikey from
  any surfaced error string.
