# IPTorrents native driver — fixtures & divergences

This is the **Native indexers** layer record for harbrr's IPTorrents (IPT) HTML-scrape
driver. It pins where the driver **knowingly differs** from the Prowlarr source it
reproduces (`Prowlarr/Prowlarr` `develop`: `IPTorrents.cs` —
`IPTorrentsRequestGenerator`, `IPTorrentsParser`, `IPTorrentsSettings`; the relative-date
helper `DateTimeUtil.FromTimeAgo`). The disposition vocabulary (`[Deliberate]` /
`[Accepted]` / `[Tracked]`) matches the AvistaZ record next door.

The golden here is **synthetic** — authored from Prowlarr's documented selector contract,
never captured from a live IPTorrents. The live Prowlarr differential and a real
search/grab are the **live-validation** gate.

## Fixtures

- `search_results.html` — a torrent-list page with a header row and two data rows (one
  freeleech, one not). The header columns are in a **deliberately non-default order**
  (`Sort by size` at index 3, not Prowlarr's positional default of 5) so `parse_test.go`
  proves the parser resolves the size/snatches/seeders/leechers columns **by header
  text**, not by a hardcoded index. The page contains `lout.php` so the `Test()` logged-in
  marker check passes, a `div.sub` relative "time ago" date, a category-icon link
  (`a[href^="?"]`), and a `download.php` link.

## Request divergences (`IPTorrentsRequestGenerator`)

- **Category param shape = `<id>=` (tracker id as the key), not `cat=`** — `[Deliberate,
  matches Prowlarr]`. Prowlarr builds the category filter with
  `qc.Add(cat, string.Empty)` where `cat` is the mapped tracker id string, so the rendered
  query is `?72=&73=` (the id is the **param name**, value empty), **not** repeated
  `cat=72&cat=73`. The driver reproduces Prowlarr exactly. (The shorthand
  "repeated `cat=`" describes the intent; the C# is authoritative and the driver follows
  it.)
- **Single `q` param (imdb + term not both emitted)** — `[Accepted]`. Prowlarr's
  `NameValueCollection` permits the same key twice, so an imdb query and a keyword term
  can each add their own `q`. harbrr's `url.Values` collapses a repeated key, so the
  driver sets the imdb `q`/`qf=all` first and then overwrites `q` with the keyword term
  only when a term is present. In practice the two are mutually exclusive (an imdb search
  carries no separate keyword term; a keyword search carries no imdb id), so the rendered
  bytes match Prowlarr for every real query. A hypothetical imdb+keyword query would send
  only the keyword `q` (Prowlarr would send both); no harbrr search path produces that
  combination.
- **`p` (page) omitted / single-page fetch** — `[Accepted]`. harbrr fetches one page and
  applies the requested `limit`/`offset` window response-side (its engine-wide paging
  mechanism, identical for every indexer). Prowlarr declares `SupportsPagination => true`
  and would add `p=` for an offset beyond the first page; harbrr's effect matches for the
  first page, and an offset beyond it yields an empty page. Per-indexer pagination would
  require an engine change harbrr does not make.
- **Search-type inference** — `[Accepted]`. harbrr's `search.Query` drops the Torznab
  `t=` function, so the keyword/episode term is built from the query fields directly: a
  season+episode renders `SxxExx`, a season-only query gets Prowlarr's trailing `*`
  wildcard, and the imdb id (when present) is sent as the Sphinx `q=+(ttNNNNNNN)` with
  `qf=all`. This reproduces Prowlarr's per-criteria request bytes in every case that
  changes results.
- **`qf=all` only on imdb** — `[Deliberate, matches Prowlarr]`. Prowlarr adds `qf=all`
  (search the description) only when an imdb id is present; a plain keyword query sends no
  `qf`. The driver matches.

## Parse divergences (`IPTorrentsParser`)

- **Column resolution by header text** — `[Deliberate, matches Prowlarr]`. The
  size/snatches/seeders/leechers columns are located by their `Sort by …` header link
  text via `findColumn`, with Prowlarr's positional fallbacks (size default 5; the
  grabs/seeders/leechers block offset 7 for a 10-cell row, else 6). The golden exercises
  the non-default order to prove this.
- **Relative "time ago" publish dates** — `[Deliberate, matches Prowlarr]`. `div.sub` is
  split on `|`, the last segment split on `" by "`, and the leading "N units ago" string
  is parsed by `parsePublishDate`, a Go port of `DateTimeUtil.FromTimeAgo`: units matched
  by substring (`sec`/`min`/`hour`|`hr`/`day`/`week`|`wk`/`month`/`mo`/`year`) or single-
  letter short form, fractional values supported, week=7d, month=30d, year=365d, "now" =>
  the current time. The result is subtracted from the **driver clock** (injected, so tests
  are deterministic). Prowlarr uses `DateTime.Now` (local); harbrr's clock is UTC in the
  registry, a normalization, not a value divergence.
- **Row with no `a.hv` or no download link skipped** — `[Deliberate]`. A row with no
  title link is Prowlarr's "no results" continue; a row with no `download.php` link is
  un-grabbable, so harbrr drops it rather than serve a feed item *arr cannot download
  (harbrr's normalizer requires an acquisition link). Prowlarr would dereference the
  missing download link and throw.
- **Missing category-icon → uncategorised, not a thrown error** — `[Accepted]`. Prowlarr
  **throws** when the category-icon link is absent (the site's "Category column" setting
  is on Text/Code rather than Icons), failing the whole page with a "change your IPT
  settings" message. harbrr instead degrades that row to an uncategorised release (empty
  `Categories`) so one mis-configured column does not blank an entire search. The operator-
  facing remedy (switch the column to Icons) is the same; harbrr just does not hard-fail.
- **`CleanTitle` third regex omitted** — `[Accepted]`. Prowlarr's title cleanup runs three
  regexes; the driver reproduces the first two (strip stray control chars; strip a
  bracketed `REQ`/`REQUEST(ED)` marker; trim spaces/dashes/colons). The third — dropping a
  **leading bracketed language group** that conflicts with anime release-group parsing
  (`^\[lang\]-Title-GROUP$`) — is a narrow .NET-regex case that harbrr's RE2-first policy
  would need `regexp2` for; it is omitted as cosmetic (it only trims a leading `[lang]`
  prefix from a small set of anime titles, never changing which release is returned).
- **`503` → rate-limit** — `[Deliberate]`. `search.IsRateLimitStatus` treats both `429`
  and `503` as a backoff trigger across the whole engine (operational safety), so a `503`
  on a search or download becomes a rate-limit error where Prowlarr would treat it as a
  plain error. Consistent with the AvistaZ driver.
- **Files column** — `[Accepted]`. The optional `Sort by files` column is read when
  present (its "Go to files" label is stripped by the digit-only `coerceInt`); when absent
  the index resolves to `-1` and `Files` stays 0, matching Prowlarr's `files = null`.

## Settings & redaction

- `cookie` (the full pasted browser Cookie string) is a `text` field whose name contains
  "cookie", so `loader.SettingsField.IsSecret()` classifies it as a secret (encrypted at
  rest, redacted by the API). It is sent **only** as the `Cookie` request header, never in
  a URL. `user_agent` is a plain `text` field (not secret-classified) sent as the
  `User-Agent` header. `redaction`-focused tests assert the synthetic cookie/UA values
  never appear in any recorded request URL or any error string.
- **Download-URL path-key redaction** — `[Accepted]`. The URL redactor is
  query-scoped, so the driver keeps the download URL out of every error it raises
  (`sanitizeGrabError`), and the `/dl` proxy keeps the raw download URL out of the served
  feed. IPT download URLs (`/download.php/{id}/{name}.torrent`) are authenticated by the
  session cookie, not a path key, so there is no path secret to leak in the first place.
  The query-scoped redactor is sufficient here; no path-aware redaction planned.

## RequestDelay

`2.1s`. `[Resolved]` — confirmed against Prowlarr source (`IPTorrents.cs`, 2026-06-21):
its IPTorrents indexer sets **no** rate-limit override, and the framework default is
`RateLimit => TimeSpan.FromSeconds(2)` (2.0s, `HttpIndexerBase`). harbrr applies a
marginally more conservative `2.1s`, riding on the definition's `RequestDelay` and
enforced by the registry's existing paced client (no special-casing). Pacing does not
affect results, so the 0.1s gap is not a parity concern.

## Live validation

- **Live search/grab + the Prowlarr differential** — `[Resolved]` (live run
  2026-06-18, `internal/smoke/README.md`): 50=50 vs the live Prowlarr, title Jaccard
  1.00, and a real `.torrent` grabbed through `/dl`.
- **Live column layout** — `[Resolved]` (same run). The 50=50 / Jaccard 1.00
  differential exercised the real header strings and the 10-vs-6 cell offset end to end,
  so the header-text column resolution holds against the live response.
