# Coverage — what harbrr serves, and the native-driver backlog

Phase 9.5 item 3. This maps, honestly, **where harbrr is a Prowlarr replacement and where it isn't**:
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

19 configured indexers — **18 torrent (all covered) + 1 usenet (out of scope)**:

| Indexer | Prowlarr impl | harbrr coverage | Live status |
|---|---|---|---|
| aura4k, darkpeers, digitalcore, lst, luminarr, onlyencodes, racing4everyone, reelflix, retromoviesclub, torrentleech, yuscene, racingforme, seedpool, upload.cx (14) | Cardigann | ✅ corpus | 13 PASS (count 1.00); seedpool was in maintenance |
| **HDSpace** | native C# | ✅ **corpus** (Jackett `hdspace.yml`) | not live-tested (see §5 extract note) |
| **FileList** | native C# | ✅ native driver | search ✅ (int-flags fix #46) |
| **IPTorrents** | native C# | ✅ native driver | ✅ count 1.00 + grab |
| **MyAnonamouse** | native C# | ✅ native driver + `mam_id` write-back | driver ✅; live pending a fresh session |
| **DOGnzb** | Newznab | ❌ **usenet** — different protocol | out of scope (harbrr is torrent/Torznab) |

**Verdict for this stack: harbrr covers every torrent indexer (18/18).** The only miss is DOGnzb, which
is **usenet/Newznab** — a protocol harbrr doesn't target (it's a torrent search provider). That's the
"dognzb doesn't work" from intake explained: not a bug, a different surface entirely.

## 4. harbrr's native-driver backlog (C# in both Jackett & Prowlarr)

Beyond this stack, the trackers harbrr can't yet serve are the ones bespoke-C# in both engines — grouped
by auth shape, mapped to the shapes harbrr **already** has, so each is "reuse" vs "new work":

| Auth shape | harbrr has it? | Backlog trackers (⭐ = popular) |
|---|---|---|
| **Bearer** (login→token) | ✅ AvistaZ family (done) | — |
| **Session cookie / form scrape** | ✅ IPTorrents (done) | ⭐TorrentDay, ⭐SpeedCD, AlphaRatio, FunFile, BitHDTV, TorrentBytes, XSpeeds, PreToMe, RevolutionTT, … |
| **Passkey / Basic / API-key (JSON)** | ✅ FileList (done) | ⭐HDBits, ⭐BeyondHD, MTeam, NorBits, SceneHD |
| **Session cookie (JSON, rotating)** | ✅ MyAnonamouse (done) | — |
| **Gazelle API** (cookie→`ajax.php`→passkey DL) | ❌ **new shape** | ⭐Redacted, ⭐Orpheus, DICMusic, Libble, GreatPosterWall, BrokenStones, … |
| **Bespoke API token** | partial | ⭐PassThePopcorn, ⭐BroadcastTheNet, ⭐GazelleGames, ⭐AnimeBytes, Nebulance |
| **Locale/parsing C#** (low priority) | n/a | RuTracker, LostFilm, Toloka, SubsPlease, AudioBookBay, … (mostly public/niche) |

**Highest-leverage next investment: one Gazelle-API base driver** — it unlocks Redacted, Orpheus, and the
whole Gazelle music/movie family in one shot (the same way the AvistaZ driver covers four sites). After
that, the cookie-scrape group (TorrentDay/SpeedCD/AlphaRatio) reuses the IPTorrents shape, and
HDBits/BeyondHD reuse the FileList passkey shape.

None of this is needed for the current stack — it's the demand-gated roadmap for when a user adds one of
these trackers. (Source: Jackett `Indexers/Definitions/*.cs` vs `Definitions/*.yml`; cross-checked against
Prowlarr `Indexers/Definitions/`.)

## 5. Migration caveat (feeds Phase 10 import)

The Prowlarr cred extractor (`scripts/prowlarr-extract-creds.sh`) maps a *Prowlarr-native* tracker to
harbrr only for the four native families it knows (AvistaZ, IPTorrents, MyAnonamouse, FileList). A
tracker that is **Prowlarr-native but harbrr-Cardigann** (like **HDSpace**) has no `definitionFile` in
Prowlarr's settings, so the extractor can't auto-map it to harbrr's `hdspace` def. harbrr *serves* it
fine; only the auto-migration needs a Prowlarr-impl → harbrr-def name table. This is a **Phase 10
migration-import** task, not a coverage gap.

## 6. Live-validation ledger (Phase 9.5 item 4)

Auth/fetch patterns proven offline but awaiting a live qualifying tracker are tracked as a standing
checklist in `internal/smoke/README.md` ("auth/fetch patterns NOT exercised live"). Current open rows:
**cookie/2FA manual-cookie**, **.NET-quirk** (`*()'!`/unicode/`regexp2`), **HTTP/SOCKS5 proxy**, and
**MyAnonamouse live search/parse** (pending a fresh session). They tick opportunistically when a
qualifying tracker appears — not a release gate.
