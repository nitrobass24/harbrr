# animebytes testdata

Golden fixtures for the native AnimeBytes driver.

- `scrape_response.json` — a populated scrape.php success body (two groups/torrents)
  used by the parse and search wiring tests.
- `empty_response.json` — an empty result body (`Matches:0`) used for the
  latest/Test probe path.

## Known limitation: no keyword music search

The native `Driver.Search` is handed only a `search.Query` (keywords, optional
artist/album/year, categories) with **no `t=` Torznab mode**. AnimeBytes' scrape.php
splits its corpus into `type=anime` and `type=music`, and `searchTypeFor` can only pick
`type=music` when an Artist or Album field is present. A keyword-only music request is
therefore indistinguishable from an anime request and would route to `type=anime`,
silently missing the music corpus.

To avoid advertising a capability that mis-routes, `MusicSearch` is **not** listed in the
caps `Modes` (see `sites.go`). Artist/Album-bearing queries still route to `type=music`
correctly; only the keyword-only music mode is unsupported.
