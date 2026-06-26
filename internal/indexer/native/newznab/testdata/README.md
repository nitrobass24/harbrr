# Newznab driver — divergence records

The generic Newznab Usenet driver. harbrr is a search provider with Cardigann-parity
on torrents; Usenet has **no Jackett behavior to match** (Jackett is torrent-only), so
the parity reference here is **Prowlarr**, and the records below are design choices,
not Jackett divergences. Indexed by [`docs/divergences.md`](../../../../../docs/divergences.md)
via the `*/testdata/README.md` glob.

## `[Deliberate]` — `.nzb` proxied server-side, not redirected

Prowlarr **301-redirects** a Usenet grab to the indexer's real download URL, which
carries the user's API key — so the key reaches the \*arr's downloader
(`NewznabController.GetDownload` forces redirect when `Protocol == Usenet`).

harbrr instead **fetches the `.nzb` server-side** through the `/dl` proxy and serves
the bytes (`Grab` returns `application/x-nzb`; `NeedsResolver()=false`,
`DownloadNeedsAuth()=true`). The API key is used inside harbrr only and never appears
in the served feed or the handed-out link. An `.nzb` is a small pointer file, so the
extra fetch is negligible, and this honors harbrr's non-negotiable secret-redaction
rule (the same posture as the torrent `/dl` passkey sealing). Exercised by
`internal/web/torznab/usenet_e2e_test.go` and `grab_test.go`. Not a gap — an
intentional improvement over Prowlarr.

## `[Deliberate]` — clean category resolution (no Prowlarr fuzzy heuristics)

Caps `<category>`/`<subcat>` are resolved to the standard Newznab table by **exact
name → exact id → Other** (subcats: combined-name → id → Parent/Other → Other/Misc).
Prowlarr adds substring/`Contains` fuzzy matching for sloppy indexer caps; harbrr
keeps the resolution exact and explicit, and would only add a heuristic if a concrete
indexer's caps required it. Exercised by `caps_test.go`.

## `[Accepted]` — no `guid` / pagination fields on the result/query

`normalizer.Release` has no `guid` field and `search.Query` carries no `limit`/`offset`,
so the driver serves the enclosure URL as the grab link and fetches a single page
(`limit=100`, Prowlarr's default). Functionally complete for v1; richer pagination is
tracked separately (`docs/plan.md`, issue #3). Not a divergence in served output.
