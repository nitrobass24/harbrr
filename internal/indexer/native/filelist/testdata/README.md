# FileList native driver — fixtures & divergences

This is the **Native indexers** layer record indexed by [`docs/divergences.md`](../../../../../docs/divergences.md).
It pins where harbrr's FileList native driver **knowingly differs** from the Prowlarr
source it reproduces (`Prowlarr/Prowlarr` `develop`: `FileListRequestGenerator`,
`FileListParser`, `FileListTorrent`, `FileListSettings`, `FileList.cs`). The disposition
vocabulary (`[Deliberate]` / `[Accepted]` / `[Tracked]`) is defined in
`docs/divergences.md`.

The goldens here are **synthetic** — derived from Prowlarr's documented contract, never
captured from a live FileList. The live Prowlarr differential and a real search/grab are
the **live-validation** gate.

## Fixtures

- `search_response.json` — a two-row api.php search response: a freeleech movie with an
  imdb id (internal flag) and a non-freeleech doubleup TV episode. Pins the base
  DTO→`normalizer.Release` mapping, the +0300 `upload_date`→UTC conversion, the
  description-keyed category map, and the rebuilt `download.php`/`details.php` URLs
  (`parse_test.go`).

## Request divergences (`FileListRequestGenerator`)

- **Download URL rebuilt, not trusted from the API** — `[Deliberate]`. The `Link` is
  built explicitly as `{base}download.php?id={id}&passkey={passkey}` (Prowlarr's
  `GetDownloadUrl`), never read from an API `download_link` field. Deterministic and
  immune to a redacted/absent link. The passkey it carries reaches only the `/dl`
  proxy (`NeedsResolver()=true`), never the served feed or a log.
- **No pagination** — `[Accepted]`. Prowlarr yields a single `IndexerRequest` (no page
  param); harbrr likewise sends one request and applies the requested `limit`/`offset`
  window response-side (its engine-wide paging mechanism). Same effect.
- **Search-type inference** — `[Accepted]`. harbrr's `search.Query` drops the Torznab
  `t=` function, so the imdb-vs-name choice and the season/episode params are inferred
  from the query fields exactly as Prowlarr's per-criteria generators do: an imdb id
  ⇒ `type=imdb` with the full id; else keywords ⇒ `type=name`; season/episode added
  only when present; a daily-episode (`yyyy MM/dd`) name search appends the
  `yyyy.MM.dd` date to the term, and a daily imdb search is skipped (→ latest-torrents),
  matching `GetSearchRequests`.
- **`genre` / music / book modes omitted** — `[Deliberate]`. Prowlarr advertises Music
  and Book `Q` search and a description-derived `Genres` list. harbrr's `search.Query`
  carries no genre field and the FileList caps here advertise basic/movie/tv search
  (the modes that change the request); the `small_description` is still stored as the
  release `Description`. Advertising a genre param harbrr would silently drop is a worse
  divergence than omitting it.
- **`passkey` typed `text` (not `password`)** — `[Accepted]`. Prowlarr marks the passkey
  `PrivacyLevel.Password`. harbrr stores it as a `text` field whose name contains the
  `passkey` secret token, so the secret store auto-classifies it as a secret (encrypted
  at rest, redacted by the API) — the same protection, via the name-token classifier
  rather than an explicit type.

## Parse divergences (`FileListParser`)

- **`imdb` normalized to `tt`+7-digit** — `[Accepted]`. Prowlarr trims the leading `t`s,
  parses to an int, and stores `ImdbId` as that int (re-rendering `tt…` at serialize
  time). harbrr stores the canonical `tt`+7-digit string directly (the same FullImdbId
  shape it sends in the request), so `"tt0133093"` and `"133093"` both normalize to
  `"tt0133093"`. A value of two characters or fewer yields empty (Prowlarr's `Length > 2`
  guard).
- **Freeleech-only filter is parser-side AND request-side** — `[Accepted]`. When the
  setting is on, the driver both sends `freeleech=1` and drops any non-freeleech row in
  the parser, mirroring Prowlarr (whose parser also skips `!row.FreeLeech` under the
  setting).
- **Volume factors** — `[Accepted]`. `freeleech` → `DownloadVolumeFactor` 0 else 1;
  `doubleup` → `UploadVolumeFactor` 2 else 1 (Prowlarr exactly).
- **Fixed `MinimumSeedTime`/`MinimumRatio`** — `[Accepted]`. 172800 s (48 h) and 1, the
  fixed values Prowlarr sets for every FileList release.
- **`503` → rate-limit** — `[Deliberate]`. `search.IsRateLimitStatus` treats `429` and
  `503` as a backoff trigger across the whole engine; Prowlarr would treat `503` as a
  plain error. harbrr's broader backoff is intentional.
- **`IndexerFlags` (Internal) dropped** — `[Accepted]`. `normalizer.Release` has no
  indexer-flags field, so the `Internal` flag Prowlarr sets is not carried. Nothing in
  harbrr's Torznab feed consumes it.
- **Bad-row handling** — `[Accepted]`. A malformed body, a `{"error":…}` envelope, or an
  unparseable `upload_date` is a parse error for the whole response, matching Prowlarr's
  throw-on-bad-row.

## Deferred to live validation

- **Live search/grab + the Prowlarr differential** — `[Tracked]`. The entire
  offline gate is synthetic; request/response/category parity against a live FileList +
  live Prowlarr, and a real `.torrent` grab through `/dl`, are the live acceptance
  gate.
- **`upload_date` shape** — `[Tracked]`. `parsePublishDate` assumes the exact
  `yyyy-MM-dd HH:mm:ss` form Prowlarr appends `+0300` to. If the live API ever emits a
  different shape, the bad-date path fails the whole search (see "bad-row handling");
  confirm the real format in the live differential.
- **Download-URL passkey redaction** — `[Tracked]`. The download URL carries the
  passkey in its query. harbrr keeps that URL out of every error it raises and routes the
  fetch only through `/dl` (which keeps the raw URL out of the served feed). Confirm the
  live download flow keeps the passkey out of all logs/traces.
