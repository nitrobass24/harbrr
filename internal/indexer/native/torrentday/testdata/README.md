# TorrentDay parser fixtures

Synthetic fixtures derived from Prowlarr's `TorrentDay.cs`
(`TorrentDayRequestGenerator` / `TorrentDayParser` / `TorrentDaySettings`,
cross-checked against Jackett's `TorrentDay.cs`) — NOT a live capture. No real
secrets appear here; the session Cookie (the driver's only secret) rides in the
request header and lives only in `*_test.go`.

- `search_results.json` — a 2-row `/t.json` array. Row 1 carries all numerics as
  JSON numbers, an `imdb-id`, and `download-multiplier: 0` (freeleech). Row 2
  carries every numeric as a JSON STRING (to exercise the tolerant `flexInt`
  decode), no `imdb-id`, and an absent `download-multiplier` (default 1).
- `empty.json` — the literal `[]` an empty result page returns (zero releases, no
  error).

Auth failure is NOT a JSON body: TorrentDay redirects to `/login.php` (HTTP 302),
so there is no error-envelope fixture — that path is exercised at the HTTP/Search
layer in a later leaf.
