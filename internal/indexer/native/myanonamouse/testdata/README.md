# MyAnonamouse native driver — fixtures & divergences

This is the **Native indexers** layer record indexed by [`docs/divergences.md`](../../../../../docs/divergences.md).
It pins where harbrr's MyAnonamouse (MAM) native driver **knowingly differs** from the
Prowlarr source it reproduces (`Prowlarr/Prowlarr` `develop`:
`MyAnonamouse.cs` — `MyAnonamouseRequestGenerator`, `MyAnonamouseParser`, and the
`MyAnonamouseSettings` POCO). The disposition vocabulary (`[Deliberate]` /
`[Accepted]` / `[Tracked]`) is defined in `docs/divergences.md`.

The goldens here are **synthetic** — derived from Prowlarr's documented contract, never
captured from a live MAM. The live Prowlarr differential and a real search/grab are the
**live-validation** gate.

## Fixtures

- `search_response.json` — a two-row `loadSearchJSONbasic.php` response. Pins the
  DTO→`normalizer.Release` mapping (`parse_test.go`): the title with appended author,
  the human-readable `size` parsed to bytes, the freeleech download factor, the
  category, and the explicit `download.php` link. One row carries a single-author
  `author_info`, the other a two-author dict, both as **stringified JSON** to exercise
  the defensive parser.

## Auth: mam_id session cookie + rotation

- **mam_id rotation is captured and written back to the store** — `[Resolved]` (#46).
  MAM rotates the `mam_id` session cookie on *every* response. The driver seeds
  `currentMamID` from `cfg["mam_id"]`, sends `Cookie: mam_id=<current>` on every request,
  and captures any refreshed `mam_id` from the response `Set-Cookie` (`auth.go`
  `captureRotatedMamID`). On a change it both updates the in-memory value **and**
  persists it back to the encrypted store via the write-back seam (`PersistSetting`), so
  the rotated session survives a process restart — matching Prowlarr (30-day write-back)
  rather than Jackett (which does not persist). The write is synchronous (the per-host
  paced doer serializes MAM requests, keeping the stored value in rotation order) and
  best-effort (a failure is logged, never fails the search). Whether the in-memory +
  write-back is *sufficient* across a restart in practice is the live question below.
- **`mam_id` redaction** — `[Deliberate]`. The `mam_id` is a secret (`password`-typed
  setting, encrypted at rest, redacted by the API). It rides only in the `Cookie` header,
  never in a URL or query, and never appears in any error string (the URL redactor plus
  `sanitizeGrabError` cover the request/grab paths). `parse_test.go`/`auth_test.go`/
  `grab_test.go` assert a distinctive synthetic `mam_id` never escapes into a recorded
  URL/query or an error.
- **403 → login failure** — `[Deliberate]`. A `403` on a search/grab/test means the
  `mam_id` expired or is invalid; it is wrapped with `login.ErrLoginFailed` (matching
  Prowlarr's "mam_id expired or invalid") so the registry records an auth_failure health
  event.

## Request divergences (`MyAnonamouseRequestGenerator`)

- **`tor[perpage]=100`, single-page fetch at `tor[startNumber]=0`** — `[Accepted]`.
  harbrr always fetches one page of 100 (no offset paging), then applies the requested
  `limit`/`offset` window response-side — its engine-wide paging mechanism, the same for
  every indexer. Prowlarr declares `SupportsPagination => true` with `PageSize = 100`;
  the per-page *bytes* line up but an `offset` beyond the first 100 yields an empty page.
- **Size/language filters omitted** — `[Accepted]`. Prowlarr can forward
  `tor[minSize]`/`tor[maxSize]`/`tor[unit]` and `tor[browse_lang][index]` from its
  size/language settings. harbrr's `search.Query` carries no size or language facet, so
  these params are omitted; results are filtered response-side by the engine where
  applicable.
- **`tor[searchType]` fixed to `all`** — `[Accepted]`. Prowlarr exposes a SearchType
  dropdown (`all`/`active`/`fl`/`fl-VIP`/`VIP`/`nVIP`). harbrr keeps the settings minimal
  (only `mam_id` plus the three search-in toggles) and always sends `all`; the freeleech
  view is a response-side concern (the download factor is parsed per row).
- **Search-in toggles** — `[Accepted]`. `title`/`author`/`narrator` are always on
  (Prowlarr's defaults); `description`/`series`/`filenames` are user checkbox toggles.

## Parse divergences (`MyAnonamouseParser`)

- **`503` → rate-limit** — `[Deliberate]`. `search.IsRateLimitStatus` treats both `429`
  and `503` as a backoff trigger across the whole engine, so a `503` on a search or
  download becomes a rate-limit error. Prowlarr would treat `503` as a plain error.
- **Human-readable `size` → bytes** — `[Accepted]`. MAM's `size` is a human string (e.g.
  `"1.29 GB"`); `parseSize` parses the amount + a case-insensitive binary (1024-based)
  unit. A missing/unknown unit or a non-numeric amount is a parse error for the whole
  response (matching Prowlarr's throw-on-bad-row).
- **`author_info` parsed defensively** — `[Accepted]`. `author_info` is a **stringified**
  (sometimes malformed) JSON dict of id→name. `authorNames` decodes it defensively: a
  decode failure yields *no* authors rather than an error or a panic. The names are
  appended to the title (`"Title by A, B"`) and set as `Release.Author`, sorted for a
  deterministic feed (the dict has no inherent order in Go).
- **`category`/`main_cat` → newznab via caps** — `[Accepted]`. The row's `category` id
  (falling back to `main_cat`) is mapped through the site caps
  (`MapTrackerCatToNewznab`, which also yields Jackett's synthesized custom `1:1`
  category, id+100000), then de-duped + sorted for a deterministic feed.
- **`added` date** — `[Accepted]`. Parsed as `"2006-01-02 15:04:05"` assuming UTC
  (Prowlarr's `ParseExact` with `AssumeUniversal`), then emitted as RFC3339 UTC.
  Prowlarr converts to *local* time; harbrr keeps UTC, the engine-wide convention. An
  unparseable date is a parse error for the whole response.
- **Freeleech → DownloadVolumeFactor** — `[Accepted]`. `free` OR `personal_freeleech`
  OR `fl_vip` ⇒ `DownloadVolumeFactor=0`, else `1`; `UploadVolumeFactor=1`,
  `MinimumRatio=1`, `MinimumSeedTime=259200` (72h, Prowlarr's fixed value).
- **`"Nothing returned, out of …"` Error → no results** — `[Accepted]`. An `Error`
  matching that prefix is treated as zero results (matching Prowlarr); any *other*
  non-empty `Error`, a missing `data` array, or a malformed body is a parse error.
- **Explicit download URL** — `[Deliberate]`. The `.torrent` URL is built explicitly as
  `{base}tor/download.php/{dl}?tid={id}` (Prowlarr's approach) rather than trusting an
  API-returned link — deterministic and immune to a redacted field. (Prowlarr appends
  `&fl=1` when the UseFreeleechWedge setting is on and the row is not already freeleech;
  harbrr does not expose that setting, so it is omitted.)
- **No infohash** — `[Accepted]`. MAM returns no infohash (the download is always an
  authenticated `.torrent` URL), so `InfoHash` is empty and the served feed routes the
  download through `/dl` (`NeedsResolver=true`).

## Request delay

- **2.1s RequestDelay** — `[Accepted]`. Prowlarr declares no explicit rate limit for
  MyAnonamouse. harbrr applies a conservative 2.1s between requests (riding on the
  definition's `RequestDelay` so the registry's existing paced client enforces it).
  Revisit against the live differential if MAM tolerates a tighter cadence.

## Live validation

- **Live search + grab** — `[Resolved]` (live 2026-06-21, through a running harbrr
  container against a fresh MAM session): the Test login probe passed, a live search
  returned well-formed releases (titles/sizes/categories/seeders/dates parsed), and the
  first result's `/dl` link resolved to a real bencoded `.torrent`
  (`application/x-bittorrent`). This supersedes the 2026-06-18 attempt, which failed at
  source (the prior `mam_id` session was dead/ASN-locked, failing in Prowlarr too).
- **Transient search-decode error** — `[Accepted]`. A few
  `decode search response: response parse error` health events were recorded once at
  2026-06-21 03:35; a follow-up the same day could **not** reproduce it across `ubuntu`,
  `linux`, `the matrix`, or a zero-result query (all HTTP 200, parsed), so it was a
  one-time transient (a non-JSON body — a rate-limit/maintenance/HTML page). The driver
  recorded it as a `parse_error` event and recovered; add a fixture only if it recurs.
- **Prowlarr differential** — `[Tracked]`. harbrr-side live search+grab is confirmed
  (above); a side-by-side count/title differential against a live Prowlarr MAM indexer is
  still the remaining parity check.
- **mam_id session lifetime across restarts** — `[Resolved]` (operator-confirmed
  2026-06-21). The write-back seam (above) persists the rotated `mam_id`, and the running
  deployment has survived several restarts and upgrades with the MAM session intact — the
  persisted-then-restarted token is accepted in practice. Today's live login + search +
  grab on that same instance corroborate it.
- **`size` unit set + `added` shape** — `[Resolved]` (live 2026-06-21). Live searches
  across several queries parsed real sizes and dates without error, so the known unit
  spellings (`parseSize` handles `B`…`PB`, both `KB` and `KiB`) and the `added` format
  held against the real API; widen the parser only if a future live response shows a shape
  these miss.
