# Native-driver roadmap

Forward-looking build plan for the trackers harbrr can only serve with a **native driver** —
the ones shipped as bespoke C# in *both* Jackett and Prowlarr, so there is no Cardigann YAML
to vendor. This sequences the demand-gated backlog by leverage; the current-state auth-shape
analysis it builds on is [`coverage.md` §4](coverage.md). Pattern: [`native-indexer-pattern.md`](native-indexer-pattern.md).

## The leverage rule

A native driver covers **one API *shape*, not one site.** Trackers running the same software
behind different hostnames share a single driver (AvistaZ is one driver serving four sites:
AvistaZ/CinemaZ/PrivateHD/ExoticaZ). So the backlog splits into **families** (one build, many
trackers) and **one-offs** (one build, one tracker). All reuse the shared native framework —
the `native.Driver` interface + registry plumbing, paced client, encrypted secrets, normalized
release, category mapper, and the authenticated `/dl` path — so each new driver is only three
pieces: a settings struct, a request generator, and a response parser. None is from scratch.

**Effort calibration** (the four shipped drivers): each is ~1.5–2.1k source LOC + ~0.8–1.1k
test LOC, **offline-gated** (stub API server + synthetic goldens derived from Prowlarr's
documented contract, never a live capture), then **live-validated** against a Prowlarr
differential + a real grab. A family base lands at the top of that range but amortizes over
every site it covers.

## Build leverage

| Build | API shape | Reuses | One build covers | ⭐ payoff | Effort |
|---|---|---|---|---|---|
| **Gazelle base** | cookie → `ajax.php` → passkey DL | 🆕 new shape | RED, OPS, DICMusic, Libble, GreatPosterWall, BrokenStones… | ⭐⭐ RED + OPS | High base, amortized over many sites |
| **Passkey/JSON** | passkey + JSON API | ✅ FileList | HDBits, BeyondHD (+ MTeam, NorBits, SceneHD) | ⭐⭐ HDBits + BeyondHD | Low–med; cheaper after the first |
| **Cookie-scrape base** | session cookie + HTML scrape | ✅ IPTorrents | TorrentDay, SpeedCD (+ AlphaRatio, FunFile, BitHDTV…) | ⭐⭐ TorrentDay + SpeedCD | Med base; each extra ≈ selectors |
| **PassThePopcorn** | bespoke movie API | framework only | PTP only | ⭐ | Med (discrete) |
| **BroadcastTheNet** | own JSON-RPC `getTorrents` | framework only | BTN only | ⭐ | Med (discrete, ~1.5–1.8k LOC) |
| **GazelleGames** | bespoke games API | framework only | GG only | ⭐ | Med (discrete) |
| **AnimeBytes** | own API | framework only | AB only | ⭐ | Med (discrete) |

The top three are families/pattern-reuses (high coverage per build); the bottom four are
discrete one-offs (one tracker each, framework reused but no shared request/parse logic).

## Recommended sequence (leverage × popularity)

1. **Gazelle base driver** — best ratio: one build unlocks RED + OPS + the Gazelle music
   family (the same multiplier as the AvistaZ driver). The highest-leverage next investment.
2. **HDBits + BeyondHD** — two ⭐ trackers reusing the FileList passkey shape; cheap.
3. **Cookie-scrape base** (TorrentDay / SpeedCD) — reuses the IPTorrents shape; two ⭐ trackers,
   each additional site mostly selectors.
4. **Bespoke one-offs, on demand** — PassThePopcorn, **BroadcastTheNet**, GazelleGames,
   AnimeBytes — each a standalone driver, built when a user actually needs it.

This is **demand-gated** — none of it is required for any current stack; it's the order to
build in when a user adds one of these trackers. A request for a *specific* tracker (e.g. BTN,
a tier-4 one-off) is built directly even though it carries no family bonus.
