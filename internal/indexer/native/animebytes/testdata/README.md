# animebytes testdata

Golden fixtures for the native AnimeBytes driver.

- `scrape_response.json` — a populated scrape.php success body (two groups/torrents)
  used by the parse and search wiring tests.
- `empty_response.json` — an empty result body (`Matches:0`) used for the
  latest/Test probe path.

## Music search

AnimeBytes' `scrape.php` splits its corpus into `type=anime` and `type=music`.
`searchTypeFor` routes to `type=music` when the query is a music search — signalled either
by the **Torznab search mode** (`search.Query.Mode == "music-search"`, set from the feed's
`t=music`) or by music params (artist/album). The `Mode` field (added to `search.Query`,
populated by the Torznab/JSON handler, and folded into the search-cache key) is what lets a
**keyword-only** music request reach the music namespace, so `MusicSearch` is advertised in
the caps `Modes` (see `sites.go`).
