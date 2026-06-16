# AvistaZ native driver — fixtures & divergences

This is the **Native indexers** layer record indexed by [`docs/divergences.md`](../../../../../docs/divergences.md).
It pins where harbrr's AvistaZ-family native driver (AvistaZ / CinemaZ / PrivateHD /
ExoticaZ) **knowingly differs** from the Prowlarr source it reproduces
(`Prowlarr/Prowlarr` `develop` @ `d6e8466`: `AvistazRequestGenerator`,
`AvistazParserBase`, the per-site `{AvistaZ,CinemaZ,PrivateHD,ExoticaZ}.cs`). The
disposition vocabulary (`[Deliberate]` / `[Accepted]` / `[Tracked: Phase N]`) is defined
in `docs/divergences.md`.

The goldens here are **synthetic** — derived from Prowlarr's documented contract, never
captured from a live AvistaZ. The live Prowlarr differential and a real search/grab are
the **Phase 9** gate.

## Fixtures

- `search_response.json` — a movie/TV/music torrents response. Pins the base
  DTO→`normalizer.Release` mapping and the descending publish-date sort
  (`parse_test.go`).
- `exoticaz_response.json` — an ExoticaZ torrents response whose rows carry the
  `category` dict. Pins the ExoticaZ category parser variant (`exoticaz_test.go`).

## Request divergences (`AvistazRequestGenerator`)

- **`tags` (genre) omitted** — `[Deliberate]`. harbrr's `search.Query` has no genre
  field, and no harbrr indexer forwards one. Advertising `genre` in the caps and then
  silently dropping it would be a worse divergence than omitting it. The four sites'
  caps therefore drop the `Genre` search param Prowlarr advertises.
- **`video_quality[]` omitted** — `[Accepted]`. Prowlarr derives `video_quality[]` from
  the *raw* newznab resolution categories (2045/2040/2030, …). harbrr's registry has
  already collapsed those to the tracker category id (`1`/`2`) before the driver's
  `Search` runs, so the resolution is unrecoverable here. The fetch is therefore by
  `type` only and the served feed is narrowed to the requested resolution by the Torznab
  category filter (`filterResults`), the same response-side mechanism harbrr uses for
  every indexer. Correct results; a broader fetch that, only under the 50-result page
  cap, could under-return a resolution-filtered query (a popular title with >50 mixed
  results). Acceptable for the offline gate; confirm at scale in the Phase 9 differential.
- **`limit` always `PageSize`, single-page fetch + `<limits max>` over-advertisement** —
  `[Accepted]`. harbrr always sends `limit=50` and one page (no `page` param), then applies
  the requested `limit`/`offset` window response-side — its engine-wide paging mechanism, the
  same for every indexer (there is no per-indexer pagination). The *effect* lines up with
  Prowlarr, which declares `SupportsPagination => false` for the family, but the *mechanism*
  differs (response-side slicing vs a declared no-pagination flag), so this is not literal
  parity. Two consequences: an `offset` beyond the first 50 yields an empty page (only 50 are
  fetched), and harbrr advertises the engine-wide `<limits max="100">` (Jackett's fixed default)
  where Prowlarr advertises `max="50"` for this family — a benign over-advertisement (the driver
  returns at most 50). Per-indexer limits would require an engine change harbrr does not make.
- **Search-type inference** — `[Accepted]`. harbrr's `search.Query` drops the Torznab
  `t=` function, so the movie/tv/basic kind is inferred from the resolved categories plus
  the id/episode fields. This reproduces Prowlarr's per-criteria request bytes in every
  case that changes results (a movie and a tv request differ only by the episode term,
  which is empty without a season/episode). A bare `imdb` query with no category and no
  season/episode is treated as a movie (Prowlarr's tv path would add an empty `search=`);
  cosmetic, no result change.

## Parse divergences (`AvistazParserBase`)

- **`503` → rate-limit** — `[Deliberate]`. `search.IsRateLimitStatus` treats both `429`
  and `503` as a backoff trigger across the whole engine (Phase 6 operational safety), so
  a `503` on a search or download becomes a rate-limit error. Prowlarr suppresses only
  `404` and would treat `503` as a plain error. harbrr's broader backoff is intentional.
- **Volume-factor default 1.0** — `[Accepted]`. An absent `download_multiply` /
  `upload_multiply` defaults to `1.0` (full cost; a freeleech torrent carries an explicit
  `0`). The API always provides both, and harbrr's serializer emits
  `downloadvolumefactor`/`uploadvolumefactor` for every release (a consumer treats an
  omitted factor as `1`), so this is semantically identical to Prowlarr.
- **Languages / subtitles dropped** — `[Accepted]`. `normalizer.Release` has no
  language/subtitle fields, so the `audio`/`subtitle` language lists Prowlarr sets are
  dropped. harbrr's Torznab feed advertises no standard language attribute, so nothing
  downstream consumes them.
- **Row with no `download` URL skipped** — `[Deliberate]`. Such a row is un-grabbable;
  harbrr's normalizer requires an acquisition link, so the driver drops it rather than
  serve a feed item \*arr cannot download. Prowlarr would include it with an empty
  `DownloadUrl`.
- **Bad-row handling** — `[Accepted]`. A malformed body, a `2xx` with no `data` array
  (a `{}`/`null`/maintenance stub — `{"data":[]}` is a legitimate empty result), an
  unparseable `created_at_iso`, or an unrecognized release `type` is a parse error for
  the whole response, matching Prowlarr's throw-on-bad-row.
- **ExoticaZ category de-dup** — `[Accepted]`. The ExoticaZ variant maps the response
  `category` dict keys through the site caps (`MapTrackerCatToNewznab`, yielding the
  standard newznab id **and** Jackett's synthesized custom `1:1` category, id+100000,
  exactly as Prowlarr's `ExoticaZParser`) and then **de-dups + sorts** the result for a
  deterministic feed; Prowlarr's `SelectMany` can emit duplicates.

## Deferred to Phase 9 (live validation)

- **Live search/grab + the Prowlarr differential** — `[Tracked: Phase 9]`. The entire
  offline gate is synthetic; request/response/category parity against a live AvistaZ +
  live Prowlarr, and a real `.torrent` grab through `/dl`, are the Phase 9 acceptance gate.
- **`created_at_iso` shape** — `[Tracked: Phase 9]`. `parsePublishDate` tries four layouts
  (`RFC3339Nano`, `RFC3339`, and the space/`T` forms *without* a zone) and normalizes to UTC,
  but **not** a space-separated datetime *with* a timezone offset (e.g.
  `2024-01-15 10:30:00+05:30`), which Prowlarr's `DateTime.Parse` accepts. If the live API uses
  that shape the bad-date path fails the **whole** search (see "bad-row handling"). Operator
  decision (2026-06-16): do **not** widen pre-emptively; confirm the real format in the Phase 9
  live differential and, if it is the space+offset form, add the `2006-01-02 15:04:05Z07:00`
  layout (a one-line fix). The risk is a hard search failure, not a silent wrong value.
- **Download-URL path-key redaction** — `[Tracked: Phase 9]`. harbrr's URL redactor is
  query-scoped, so the driver keeps the download URL out of every error it raises (a key,
  if any, may sit in the path). The `/dl` proxy already keeps the raw download URL out of
  the served feed. Confirm the live download-URL shape and, if it carries a path key,
  decide whether path-aware redaction is warranted.
