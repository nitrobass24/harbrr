# Nebulance native driver — fixtures & divergences

The synthetic `search_response.json` fixture pins the current Prowlarr Nebulance contract:
`GET /api.php?action=search`, API-key query auth, upstream `page` / `per_page` paging, the
`current_page` / `total_pages` / `count` / `total_results` + `items` envelope, TV-quality
category inference, ratioless volume factors, season/episode minimum seed times, external IDs,
and `api.php?action=download&apikey=…&torrentid=…` acquisition URLs.

## Validation status

- **[Accepted] Offline fixture** — synthetic values only. Its shape was checked against a
  locally supplied, redacted NBL response; no live response, release name, or credential is stored.
- **[Accepted] Download URL** — NBL supplies the acquisition URL. harbrr keeps it behind `/dl`
  and fetches it server-side so its token never reaches clients or logs.
- **[Accepted] Scene flag** — Prowlarr derives `Scene` from the response tags. harbrr's canonical
  release model has no scene field, so the tag is not emitted in the Torznab result.
- **[Tracked] Live differential pending** — request/parse/grab behavior is offline-gated against
  the documented Prowlarr and Jackett implementations. A real NBL account is still required for
  the Prowlarr differential and live grab.
