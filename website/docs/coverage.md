# Tracker coverage

harbrr serves the **search** surface of your trackers — on-demand `q=…` queries returned as
Torznab/Newznab, the same feed Sonarr, Radarr, autobrr, and cross-seed consume. It does **not**
sit on a tracker's IRC **announce** firehose — that's autobrr's job — so for a tracker with an
announce channel you'd run both (autobrr for grab-on-announce, harbrr for on-demand search).

## Will harbrr serve my tracker?

Almost certainly — if Prowlarr or Jackett can, harbrr very likely can too. Coverage comes from
two places:

1. **The Cardigann definition corpus.** harbrr embeds the full Jackett tracker-definition set
   (500+ trackers) and runs it through a Cardigann-compatible engine. Any tracker shipped as a
   Cardigann YAML definition works out of the box — no per-tracker code. This is the large
   majority of trackers. See **[Adding an indexer](guides/add-indexer.md)**.

2. **Native drivers.** A handful of trackers are built as bespoke code (no Cardigann definition)
   in Jackett and Prowlarr; harbrr ships native Go drivers for those. Shipped today:

   > AvistaZ · CinemaZ · PrivateHD · ExoticaZ · IPTorrents · MyAnonamouse · FileList ·
   > BroadcastTheNet · Redacted · Orpheus · PassThePopcorn · GazelleGames · AnimeBytes ·
   > HDBits · BeyondHD · TorrentDay — plus generic **Usenet (Newznab)** indexers.

## Not yet supported — vote for yours

These trackers are bespoke code in both Jackett and Prowlarr and don't yet have a harbrr native
driver. They're **demand-gated**: 👍 or comment on the issue for the one you want and it moves up
the queue. If you have an account and can help test, say so on the issue.

**Private (cookie-scrape):**
[SpeedCD](https://github.com/autobrr/harbrr/issues/21) ·
[AlphaRatio](https://github.com/autobrr/harbrr/issues/22) ·
[FunFile](https://github.com/autobrr/harbrr/issues/23) ·
[BitHDTV](https://github.com/autobrr/harbrr/issues/24) ·
[TorrentBytes](https://github.com/autobrr/harbrr/issues/33) ·
[XSpeeds](https://github.com/autobrr/harbrr/issues/34) ·
[PreToMe](https://github.com/autobrr/harbrr/issues/35) ·
[RevolutionTT](https://github.com/autobrr/harbrr/issues/36)

**Private (passkey / JSON API):**
[MTeam](https://github.com/autobrr/harbrr/issues/25) ·
[NorBits](https://github.com/autobrr/harbrr/issues/26) ·
[SceneHD](https://github.com/autobrr/harbrr/issues/27)

**Gazelle music (username / password):**
[DICMusic](https://github.com/autobrr/harbrr/issues/28) ·
[Libble](https://github.com/autobrr/harbrr/issues/29) ·
[GreatPosterWall](https://github.com/autobrr/harbrr/issues/30) ·
[BrokenStones](https://github.com/autobrr/harbrr/issues/31)

**Bespoke:**
[Nebulance](https://github.com/autobrr/harbrr/issues/32)

**Public / niche:**
[RuTracker](https://github.com/autobrr/harbrr/issues/37) ·
[LostFilm](https://github.com/autobrr/harbrr/issues/38) ·
[Toloka](https://github.com/autobrr/harbrr/issues/39) ·
[SubsPlease](https://github.com/autobrr/harbrr/issues/40) ·
[AudioBookBay](https://github.com/autobrr/harbrr/issues/41)

Don't see yours? [Open an issue](https://github.com/autobrr/harbrr/issues/new) and describe the
tracker — if it's a Cardigann definition it may already work; if it needs a native driver, it
joins the list above.
