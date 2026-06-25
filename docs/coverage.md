# Coverage — what harbrr serves, and the native-driver backlog

This maps, honestly, **where harbrr is a Prowlarr replacement and where it isn't**:
the surface harbrr covers, how its coverage is sourced, the user's actual stack, and the real
native-driver backlog (vs Prowlarr's full set). Companion to `docs/native-indexer-pattern.md` (how a
native driver is built) and the live-validation ledger in `internal/smoke/README.md`.

## 1. Surfaces — harbrr owns *search*, not announce

A private tracker exposes several independent surfaces, each a different endpoint with different auth.
Different tools own different surfaces — they are **complementary, not substitutes**:

| Surface | What it is | Tool that owns it | harbrr? |
|---|---|---|---|
| **Search** | on-demand `q=…` query → Torznab/Newznab results | **harbrr / Prowlarr / Jackett** | ✅ this is harbrr |
| **Announce** | the IRC firehose of every new upload (push, low-latency) | **autobrr** (autodl-irssi) | ❌ out of scope |
| **RSS** | the tracker's "latest N" feed, polled | autobrr (fallback), the \*arrs, Prowlarr/Jackett | ✅ via the served feed |

harbrr is a **search** provider (Cardigann-parity Torznab/Newznab). It does **not** sit on IRC announce —
that's autobrr's job, and you'd run both for the same tracker (autobrr for grab-on-announce, harbrr for
\*arr-driven search). This doc is only about the **search** surface.

## 2. How harbrr's coverage is sourced (and the Prowlarr-native ≠ harbrr-gap rule)

harbrr serves a tracker two ways:
- **Cardigann corpus** — it vendors **Jackett's** YAML definitions byte-for-byte (558 defs). Any tracker
  Jackett ships as YAML, harbrr serves through the engine.
- **Native Go drivers** — only for trackers with **no** Cardigann YAML (`internal/indexer/native/`).

**Key rule:** Prowlarr and Jackett disagree on what's "native." Prowlarr has moved many trackers to
bespoke C#, but **Jackett often still ships YAML for them** — and harbrr vendors Jackett. So a
*Prowlarr-native* tracker is **not** automatically a harbrr gap. Example: **HDSpace** is C# in Prowlarr
but `hdspace.yml` in Jackett, so harbrr serves it via the corpus, no driver needed.

⇒ harbrr's **true** native gap = trackers that are bespoke C# in **both** Jackett **and** Prowlarr.

## 3. The stack (this deployment's Prowlarr indexers)

19 configured indexers — **18 torrent (all covered) + 1 usenet (now covered via the Newznab driver)**:

| Indexer | Prowlarr impl | harbrr coverage | Live status |
|---|---|---|---|
| aura4k, darkpeers, digitalcore, lst, luminarr, onlyencodes, racing4everyone, reelflix, retromoviesclub, torrentleech, yuscene, racingforme, seedpool, upload.cx (14) | Cardigann | ✅ corpus | 13 PASS (count 1.00); seedpool was in maintenance |
| **HDSpace** | native C# | ✅ **corpus** (Jackett `hdspace.yml`) | not live-tested (see §5 extract note) |
| **FileList** | native C# | ✅ native driver | search ✅ (int-flags fix #46) |
| **IPTorrents** | native C# | ✅ native driver | ✅ count 1.00 + grab |
| **MyAnonamouse** | native C# | ✅ native driver + `mam_id` write-back | driver ✅; live pending a fresh session |
| **DOGnzb** | Newznab | ✅ **generic Newznab driver** (Phase 11) | offline-built; live pending a usenet apikey |

**Verdict for this stack: harbrr now covers all 19/19.** The 18 torrent indexers were always covered;
DOGnzb (**usenet/Newznab**) is served as of Phase 11 by the generic Newznab driver
(`internal/indexer/native/newznab/`) — harbrr is no longer torrent-only. Jackett still can't serve it
(Jackett is torrent-only), so this is parity with **Prowlarr**, the sole usenet parity target. Live
validation is deferred until a real usenet apikey is in the stack (opportunistic, not a gate — same
posture as the offline-built native torrent drivers in §4).

## 4. harbrr's native-driver backlog (C# in both Jackett & Prowlarr)

Beyond this stack, the trackers harbrr can't yet serve are the ones bespoke-C# in both engines — grouped
by auth shape, mapped to the shapes harbrr **already** has, so each is "reuse" vs "new work":

| Auth shape | harbrr has it? | Remaining backlog (⭐ = popular) |
|---|---|---|
| **Bearer** (login→token) | ✅ AvistaZ family | — |
| **Session cookie / form scrape** | ✅ IPTorrents · ✅ TorrentDay (#63, untested) | ⭐SpeedCD, AlphaRatio, FunFile, BitHDTV, TorrentBytes, XSpeeds, PreToMe, RevolutionTT, … |
| **Passkey / Basic / API-key (JSON)** | ✅ FileList · ✅ HDBits, BeyondHD (#63, untested) | MTeam, NorBits, SceneHD |
| **Session cookie (JSON, rotating)** | ✅ MyAnonamouse | — |
| **Gazelle API** (cookie/key→`ajax.php`) | ✅ Redacted, Orpheus (#63) | DICMusic, Libble, GreatPosterWall, BrokenStones (username/password login — different auth) |
| **Bespoke API token** | ✅ BroadcastTheNet (#62) · ✅ PassThePopcorn, GazelleGames, AnimeBytes (#63, untested) | Nebulance |
| **Locale/parsing C#** (low priority) | n/a | RuTracker, LostFilm, Toloka, SubsPlease, AudioBookBay, … (mostly public/niche) |

> **#63 batch — built but LIVE-UNTESTED.** Redacted, Orpheus, PassThePopcorn, GazelleGames, AnimeBytes,
> HDBits, BeyondHD, and TorrentDay are implemented and **offline-gated** (synthetic goldens from the
> documented autobrr/Prowlarr contracts) but **not yet live-validated** — the operator has no credentials
> for them. They degrade cleanly (parse/auth errors → health events); live validation (Prowlarr
> differential + a `/dl` grab) is a per-tracker follow-up when creds exist, tracked in
> `internal/smoke/README.md`.

**Remaining high-leverage work:** the cookie-scrape group (SpeedCD + AlphaRatio/FunFile/… reuse the
IPTorrents/TorrentDay shape) and the passkey group (MTeam/NorBits/SceneHD reuse FileList). The
username/password Gazelle sites (DICMusic/GPW/BrokenStones) need a login-flow addition to the Gazelle
driver. **SpeedCD is HTML-scrape — deferred** until page samples or creds exist (guessing selectors
untested is net-negative).

None of this is needed for the current stack — it's the demand-gated roadmap for when a user adds one of
these trackers. (Source: Jackett `Indexers/Definitions/*.cs` vs `Definitions/*.yml`; cross-checked against
Prowlarr `Indexers/Definitions/`.) **Build sequencing + per-driver effort:** see [`native-roadmap.md`](native-roadmap.md).

## 5. Migration caveat (feeds the Prowlarr migration import)

The Prowlarr cred extractor (`scripts/prowlarr-extract-creds.sh`) maps a *Prowlarr-native* tracker to
harbrr only for the four native families it knows (AvistaZ, IPTorrents, MyAnonamouse, FileList). A
tracker that is **Prowlarr-native but harbrr-Cardigann** (like **HDSpace**) has no `definitionFile` in
Prowlarr's settings, so the extractor can't auto-map it to harbrr's `hdspace` def. harbrr *serves* it
fine; only the auto-migration needs a Prowlarr-impl → harbrr-def name table. This is a
**migration-import** task, not a coverage gap.

## 6. Live-validation ledger

Auth/fetch patterns proven offline but awaiting a live qualifying tracker are tracked as a standing
checklist in `internal/smoke/README.md` ("auth/fetch patterns NOT exercised live"). Current open rows:
**cookie/2FA manual-cookie**, **.NET-quirk** (`*()'!`/unicode/`regexp2`), **HTTP/SOCKS5 proxy**, and
**MyAnonamouse live search/parse** (pending a fresh session). They tick opportunistically when a
qualifying tracker appears — not a release gate.
