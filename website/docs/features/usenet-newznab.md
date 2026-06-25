# Usenet (Newznab) indexers

harbrr isn't just for torrents. It can serve your **Usenet indexers** too — NZBgeek,
DOGnzb, NZBFinder, DrunkenSlug, or any server that speaks the standard **Newznab**
API — right alongside your trackers, through the same Torznab/Newznab feed your
\*arr apps already use.

Add a Usenet indexer once in harbrr, and Sonarr, Radarr, and the rest can search it,
grab from it, and hand the `.nzb` to SABnzbd or NZBGet — exactly as if you'd added it
to Prowlarr.

---

## Why this matters

Prowlarr can manage both torrents and Usenet; Jackett is torrent-only. So if your
stack has even one Usenet indexer, Jackett alone can't replace Prowlarr for you — and
neither could harbrr, until now. With Newznab support, harbrr serves your **whole**
indexer set — torrent and Usenet — so it's a complete Prowlarr replacement, not a
torrent-only one.

You also get harbrr's usual upside on top: one place to hold the indexer's API key,
one feed your apps share, and harbrr's caching and tracker-friendly pacing working
the same way for Usenet as for torrents.

---

## How to add one

You add a Usenet indexer the same way you add any indexer in harbrr:

1. **Pick the definition.** Choose **Newznab** (the generic option) for any server, or
   one of the built-in presets (NZBgeek, DOGnzb, NZBFinder, …) which pre-fill the
   server URL for you.
2. **Fill in the URL and API key.** For the generic option, paste your indexer's base
   URL (e.g. `https://api.nzbgeek.info`); for a preset, the URL is already set. Then
   paste your **API key** from the indexer's profile page.
3. **Test and save.** harbrr fetches the indexer's capabilities and category list
   straight from the server, validates your key, and you're done.

That's it. The indexer now appears in your feed and capabilities, and syncs into
Sonarr/Radarr as a Usenet indexer (so grabs are routed to your Usenet download
client, not your torrent client).

---

## Your API key stays inside harbrr

This is the one place harbrr deliberately does **more** than Prowlarr.

A Newznab download link has your API key baked into the URL. Prowlarr, when an app
grabs a release, **redirects** the app's downloader to that real URL — so your API key
travels on to SABnzbd/NZBGet.

harbrr doesn't. When a grab comes in, harbrr fetches the `.nzb` **itself**,
server-side, and hands your app just the file. Your API key is used inside harbrr and
**never appears** in the feed your apps see, nor in the link they're handed. An `.nzb`
is a tiny pointer file, so fetching it this way costs nothing noticeable — and your
key stays where it belongs.

(This is the same `/dl` proxy harbrr already uses to keep tracker passkeys out of
torrent feeds.)

---

## What's supported

- **Any Newznab server** via the generic **Newznab** definition, plus presets for the
  popular indexers — the same coverage model as Prowlarr.
- **Search modes** — basic, TV, movie, music, and book search, with the ID lookups
  (IMDb, TMDb, TVDb, …) your indexer advertises in its capabilities.
- **Categories** — discovered live from the indexer's own capabilities, so its
  category tree maps correctly into your \*arr apps.
- **App sync** — Usenet indexers push into Sonarr/Radarr as Usenet indexers
  automatically. (qui is torrent-only, so Usenet indexers are simply skipped there.)

NZBHydra2 works too — it speaks Newznab, so the generic option covers it.
