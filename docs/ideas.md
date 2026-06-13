# harbrr — design & technical plan

**harbrr is a lightweight, Go-native Jackett/Prowlarr-style search provider built for the autobrr
ecosystem — starting from Cardigann compatibility instead of a new tracker format.**

> Status: design / pre-build. Built first in `nitrobass24/harbrr` to prove the core, then intended
> to move to the **autobrr org** once viable and with the team's buy-in.
> License: **GPL-2.0-or-later** (matches autobrr / qui).

---

## 1. Executive summary

harbrr gives the **autobrr family a native indexer search/scrape provider** — the role the family
currently outsources to **Prowlarr**, an external .NET service its own documentation tells users to
install. harbrr fills that slot natively: it exposes Torznab/Newznab and on-demand search over the
same private and public trackers, built on the substrate the family already maintains (`rls`,
`go-qbittorrent`, the qui patterns). It is a **single binary for normal operation**; only
anti-bot-protected indexers need an optional external solver (see §6, §9).

The project succeeds or fails on **one** thing: faithfully reimplementing the **Cardigann definition
engine** — the declarative per-tracker adapter format Jackett and Prowlarr share — closely enough to
the .NET original that the existing **community corpus of 550+ tracker definitions runs
unmodified**. Everything else (Torznab output, application sync, a UI) is comparatively
well-understood plumbing. The engine is the risk — and it is a *deeper* risk than "parse YAML and
pass Jackett's tests": it means reproducing years of accidental behavior, tracker-specific hacks, and
HTTP/session edge cases. The saving grace is the validation strategy (§7): because the correctness
target is **Jackett specifically**, those quirks never have to be *enumerated or understood* — feeding
the same saved input to both engines and diffing reproduces them automatically. The long tail is
intractable to catalog but tractable to *match*. That is why the plan is **test-harness first, engine
second, product third**.

Choices already settled:

- **Vendor Jackett's GPL-2.0 definition corpus** (558 defs), not Prowlarr's (unlicensed). This keeps
  redistribution clean and makes the dependency a *community* one rather than a Prowlarr one.
- **Consume definitions byte-for-byte** and absorb every behavioral divergence in the engine, never
  in the def files.
- **Hand-authored OpenAPI** the qui way (embedded spec + Swagger UI + drift tests); **GPL-2.0-or-later**.
- **SQLite first**; build a narrow proof of Cardigann parity before any product surface.

## 2. Why this exists

Two gaps:

1. **The family outsources search to an external .NET dependency.** autobrr handles real-time IRC
   announces and pushes releases to download clients and the *arr apps; for the many trackers without
   an IRC announce channel, autobrr's own docs recommend running **Prowlarr** as a Torznab/Newznab
   feed source. An otherwise Go/React, SQLite/Postgres, GPL-2 family thus leans on a heavyweight .NET
   application for a core function.
2. **Prowlarr is slow to accept the integrations this ecosystem wants**, and is structurally a large
   .NET/React app the family can't easily extend. The integrations that matter here — pushing to
   autobrr, acting as a cross-seed backend, first-class qBittorrent — are not upstream priorities.

harbrr makes the search/scrape function **native to the family**, on a stack it already maintains,
and open to the integrations Prowlarr resists.

## 3. What harbrr is / is not

**Is:**

- The family's **native Torznab/Newznab + on-demand-search provider** (the slot Prowlarr fills today).
- A **Cardigann-compatible engine** that runs the existing community definition corpus unmodified.
- A **single binary for normal operation**, SQLite by default, spec-first (OpenAPI), GPL-2.0-or-later.
- **Open to family integrations**: feeding autobrr, backing cross-seed, pushing to qBittorrent via
  `go-qbittorrent`.

**Is not:**

- An **IRC announce** tool — that is autobrr's job; harbrr integrates with it, not replaces it.
- A **torrent creator** (mkbrr), an **uploader** (upbrr), or a **qBittorrent UI** (qui).
- A **new or competing definition format** — it speaks Cardigann deliberately, for interoperability.
- A **full Prowlarr clone**, or a drop-in for every Prowlarr feature on day one. (See §13 for how the
  project is positioned at first public release — *not* as a "Prowlarr replacement".)
- A **plugin platform** — the extension model is an open API plus pull requests.
- **Self-contained for protected indexers** — Cloudflare-gated trackers need an external solver.

## 4. MVP and non-goals

The MVP is deliberately boring: a test harness and an engine that can impersonate Jackett on saved
inputs, then just enough product to be usable. Concretely:

1. **Vendored Jackett corpus loader.**
2. **Offline Cardigann conformance runner** — the full extraction pipeline over saved fixtures.
3. **Fixture-based result normalizer** validated against Jackett's output.
4. **Auth/session** for the four login methods (`form`/`post`/`get`/`cookie`), tested offline against
   saved login sequences, with manual cookie-paste fallback.
5. **Torznab caps + search output** (capabilities/category correctness is a gate, not just result
   XML — see §8).
6. **5 live tracker smoke tests.**
7. **Docker image + config file.**

**Non-goals for v1:** Postgres (the interface exists; the implementation does not — §6); ***arr
application sync**, Jackett/Prowlarr **migration**, **web-UI**, **stats / notifications**; **native
indexers including Avistaz** (a post-parity milestone, not part of the engine proof — §6); the
**shared family registry**; any **plugin runtime**.

## 5. Core technical risks

In rough order of how much each can sink the project:

1. **Cardigann engine parity (existential).** The engine must interpret each definition exactly as
   the .NET original does. Divergence means silently wrong results against real trackers. The honest
   framing: this is not a bounded parsing problem but the reproduction of years of accidental
   behavior. *Mitigation:* the target is **Jackett-equivalent normalized release objects**, validated
   by **differential testing against Jackett's engine offline** — so the quirks are matched, not
   catalogued (§7).
2. **Regex semantics (.NET vs Go).** RE2 is not .NET's backtracking engine. *Mitigation:* a hybrid
   with an explicit, implementable routing rule (§7) — RE2 by default for its ReDoS safety, regexp2
   for .NET-only constructs and locale-sensitive defs, both engines run against the same fixtures.
3. **Validation without live tracker access.** *Mitigation:* parity with Jackett's engine on the same
   input, fully testable offline; per-definition-vs-live correctness is the corpus's job.
4. **Corpus dependency & licensing.** *Mitigation:* depend on the Cardigann *community*; vendor
   Jackett's GPL-2.0 corpus (§11). Hedge: a shared family registry. Break-glass: self-maintain.
5. **Auth/session breadth.** 83% of definitions authenticate. *Mitigation:* treat session management
   as **core engine behavior, not product polish** — it is a pipeline module (§7) tested offline, and
   it moves early in the roadmap (§12). Manual cookie injection is the universal fallback.
6. **Anti-bot upkeep.** A perpetual arms race where Go offers no advantage. *Mitigation:* **delegate
   to FlareSolverr** behind a pluggable solver interface, reached only for protected indexers.
7. **Secret handling.** harbrr holds passkeys, cookies, API keys, and download tokens — a real
   attack surface that an indexer manager must take seriously from the start (§9).

## 6. Architecture

### Subsystem map

| Subsystem | Responsibility | Note |
|---|---|---|
| Cardigann engine | Definition parsing, capabilities, category system, Torznab/RSS parsing, magnet building | The core; built as a pipeline (§7) |
| Definitions | Vendored Cardigann YAML **+** native Go for sites YAML can't express | Lifecycle in §10 |
| Indexer search | Fan a query across indexers, merge, page | Go concurrency fits well |
| **Auth / session** | `login:` handlers (form/post/get/cookie), CSRF, error/test selectors, cookie jar, re-login | First-class engine module — 83% of defs authenticate; manual cookie fallback |
| Indexer proxies | FlareSolverr / HTTP / SOCKS per indexer | Anti-bot delegated behind a pluggable solver interface; HTTP/SOCKS native |
| Applications + Profiles | Sync indexers into the *arr apps | Product phase; family integration point (§11) |
| Download | Download-client clients + push-release | `go-qbittorrent` |
| Stats / History | Counters; query/grab/auth event log | Storage + query API |
| Notifications | Discord/webhook | Pluggable provider |
| Datastore | SQLite, migrations, repos | Behind one interface — SQLite first (below) |
| Scheduler / event bus / lifecycle | Jobs, messaging, startup/shutdown | Standard |

The **provider pattern** (Prowlarr's `ThingiProvider`) is carried over: everything pluggable is a
provider with a typed settings schema, validation, and a test action — a small set of Go interfaces
plus a registry, and exactly the seam where harbrr can be open where Prowlarr is conservative.

### Stack

| Concern | Prowlarr | harbrr |
|---|---|---|
| Language / runtime | C# / .NET | Go, single static binary, no cgo |
| HTTP router | ASP.NET | `chi` |
| API spec | hand-maintained | hand-authored `openapi.yaml` + `//go:embed` + Swagger UI + drift tests (qui) |
| HTML scraping | AngleSharp | `goquery` / `cascadia` (verified continuously — §7) |
| Regex | .NET regex | `regexp` (RE2) + pure-Go `regexp2` fallback (§7) |
| Database | SQLite/Postgres | `modernc.org/sqlite` first, behind an interface |
| Release parsing | `Parser` | `autobrr/rls` (family) |
| qBittorrent | own client | `autobrr/go-qbittorrent` (family) |
| Realtime | SignalR | SSE via `tmaxmax/go-sse` (qui) |
| Logging / config / CLI | NLog / custom | `zerolog` / `viper` / `cobra` (qui / family) |

### Project layout (sketch)

```
cmd/harbrr/            # entrypoint (cobra/viper)
internal/
  web/ swagger/         # chi router; openapi.yaml (//go:embed) + Swagger UI + drift tests
  indexer/
    cardigann/          # the engine pipeline (loader → … → serializer; see §7)
    native/             # base families YAML can't express + protocol bases + one-offs
    definitions/        # //go:embed vendored Jackett snapshot + drop-in/override dir
  search/               # fan-out, merge, paging
  http/                 # auth/session, cookie jar, FlareSolverr solver interface, log redaction
  download/             # download-client clients + push-release (go-qbittorrent)
  secrets/              # at-rest encryption, redaction helpers (§9)
  database/ dbinterface/ # sqlite (default) behind one interface; Postgres later
```

### Database — interface from day one, SQLite first

The DB layer uses the qui `dbinterface` pattern so a second backend can be added without rework, but
**only SQLite is implemented**, and the roadmap makes no Postgres commitment — the interface simply
keeps it *possible* later. Dual-database behavior (migrations, dialect differences, a doubled test
matrix) is real ongoing cost that would distract from the engine, which is where the risk lives.

### Native indexers — a post-parity milestone

Measured against the vendored corpus: **UNIT3D ≈ 80 defs, Gazelle ≈ 6 defs, Avistaz = 0 defs.** Native
UNIT3D/Gazelle drivers would only rebuild coverage already vendored for free, so they are skipped. The
**Avistaz family** is the genuine gap (0 defs, because its login→Bearer `api/v1/jackett` auth exceeds
the declarative format) — but it is **strategically interesting, not necessary to retire Cardigann
risk**, so it is explicitly a *post-parity* milestone, not part of the engine proof. Protocol bases
(Newznab/Torznab/TorrentPotato/TorrentRss) you build anyway. Release-name parsing is **not** ported —
the family's `rls` does it.

## 7. Cardigann engine plan

A definition is one YAML file describing how to talk to one tracker: **header**, **caps** (categories
+ search modes), **settings**, **login** (`post`/`get`/`form`/`cookie` + error/test selectors),
**search** (`paths`/`inputs`/`rows`/`fields`, in HTML/JSON/XML), and **download** (pre-request or
magnet/infohash synthesis). The engine is written **fresh** (the 2018 Go `cardigann` is abandoned)
but implements the same Cardigann format — the name is the interoperability contract, so it is kept.
Definitions are **never edited**; all behavioral differences live in the engine.

### Build it like a compiler pipeline

The engine is a sequence of independently testable modules, never a blur:

`YAML schema loader` → `capability/category mapper` → `template evaluator` → `filter registry` →
`selector adapter` → `date parser` → `regex adapter` → `login/session executor` → `search executor`
→ `result normalizer` → `Torznab serializer`.

Each module has its own fixtures. Notably, the **login/session executor is one of these modules** —
which is why auth is engine behavior tested offline against saved login sequences, not a later product
phase (§5, §12).

### The pieces that carry the risk

- **Regex — RE2 by default, regexp2 on demand, with an implementable rule.** You *cannot* statically
  prove an arbitrary pattern matches identically under RE2 and .NET (equivalence is undecidable), so
  "RE2 only when provably equivalent" would force everything onto regexp2 and discard RE2's two real
  benefits: linear-time matching and **ReDoS safety** (regexp2 backtracks and can be hung by a hostile
  def or tracker response, bounded only by `MatchTimeout`). The rule: **RE2 is the default for its
  ReDoS guarantee; route to regexp2 when** the definition opts in, the tracker's `language:` is
  non-Latin, the pattern fails RE2 compilation, or the pattern uses .NET-only constructs
  (backreferences, lookarounds, atomic/conditional groups, `(?<name>)`). The differential suite runs
  **both engines on the same fixtures** and is the gate that catches any silent RE2 ≠ .NET case; when
  found, that pattern's def is added to the regexp2 routing.
- **Dates — the deepest rabbit hole.** Definitions use .NET format strings; a dedicated translator
  maps .NET tokens to Go's reference-time layout, and must also handle **timezone tokens, relative
  dates ("today"/"yesterday"/"N minutes ago"), localized month and day names** (the corpus spans many
  non-English locales), and tracker-specific date weirdness. Gets its own large fixture suite.
- **Templates and filters.** Go `text/template` plus a small filter registry implementing the bounded
  Cardigann vocabulary to .NET-equivalent behavior, including empty-vs-missing truthiness.
- **Selectors — verified continuously.** `cascadia` (goquery's engine) supports the Sizzle selector
  vocabulary the defs use, but cascadia is **not** AngleSharp/Sizzle; edge cases (`:contains`
  whitespace/case, `:has` scoping) will differ until pinned by **its own fixture suite**, which is
  treated as a standing compatibility check, not a one-time verification.

### Validation strategy

The correctness target is **Jackett-equivalent normalized release objects on the same input** — not
matching live trackers (a wrong definition is wrong in Jackett too, fixed in the corpus on
re-vendor). Validation is almost entirely **offline**:

- **Port Jackett's GPL-2.0 Cardigann engine tests** — a ready-made conformance oracle.
- **Structural schema validation** of every vendored definition at build time (failures triaged, never
  silently dropped — §12).
- **Golden-file fixtures**, one per engine primitive (each filter, template function, date format,
  selector extension).
- **Differential testing** on the few trackers you have accounts on: run Jackett and harbrr over the
  same saved response, diff.
- **Live smoke canaries** — integration checks, not the parity oracle.

The bar for "parity is real" is the compatibility matrix in §12.

## 8. Protocol / API layer

Two distinct contracts, often conflated:

- **Torznab / Newznab — the *arr-facing contract.** XML, defined by the Torznab/Newznab spec, not by
  harbrr's OpenAPI. It is what Sonarr/Radarr and autobrr's Feeds actually consume. **Capabilities and
  category correctness is an MVP gate, not just result-XML shape** — Sonarr/Radarr integration
  failures usually trace to caps/category behavior, not the result envelope.
- **harbrr's own management API — OpenAPI/Swagger.** A hand-authored `openapi.yaml`, `//go:embed`-ed,
  served with Swagger UI, with drift tests asserting spec-matches-handlers (the qui pattern). This
  governs configuration/management, not the *arr integration.

## 9. Security model

harbrr stores tracker **passkeys, cookies, API/auth keys, and download tokens**, plus its own
**web-UI login and management API keys** — among the most sensitive data a self-hosted app holds, and
the place a careless indexer manager leaks first. The model **follows qui** (the autobrr-family
sibling harbrr is patterned on for API/DB/security): three credential *classes*, handled three
different ways. Conflating them — especially storing a login password the same way as a tracker
passkey — is the classic mistake, so the split is structural, not incidental.

### Credential classes (the core rule)

| Class | Examples | Mechanism | Recoverable? |
|---|---|---|---|
| **Login password** | the web-UI/admin password | **argon2id** (m=64 MiB, t=3, p=2, 16-byte salt, 32-byte key), PHC-string encoded, constant-time verify | **No** — one-way hash |
| **Bearer tokens** | management API keys, the *arr-facing Torznab `apikey`, session tokens | random 32 bytes, stored as a **SHA-256 hash**, shown to the user **once** | **No** — one-way hash |
| **Tracker credentials** | passkeys, login user/pass, cookies, tracker API keys, download tokens | **AES-256-GCM** at rest (harbrr must *replay* them to log into the tracker) | **Yes** — decrypted at request time |

Rule of thumb: **anything harbrr must replay is encrypted; anything it only needs to verify is
hashed.** The web-UI password and API keys are never stored in recoverable form, so a database leak
(or even a key compromise) never yields them.

### At-rest encryption (tracker credentials)

- **AES-256-GCM** (`crypto/aes` + `crypto/cipher`), 32-byte key, a **fresh random nonce per record**
  from `crypto/rand`, stored prepended to the ciphertext (`nonce‖ciphertext‖tag`, base64) in
  `*_encrypted` columns — qui's construction.
- **AAD bound to the row identity** (`indexer_id` + setting name) so a ciphertext cannot be copied or
  replayed across rows/fields. *(qui passes no AAD; harbrr adds it — a near-free hardening.)*
- A **`key_id` is stored with every record from day one**, so key rotation is possible later even
  though rotation itself is post-MVP. Nearly free now, impossible to retrofit cleanly.

### Key management

- The 32-byte key comes from a configured source — `secrets.encryption_key` (inline/env) or
  `secrets.key_file` (path), already modeled in `internal/config` — **kept separate from the session
  secret**. *(qui derives its AES key from the session secret and flags that as improvable; harbrr
  keeps the two distinct.)*
- **First run with no key configured AUTO-GENERATES a keyfile** (32 random bytes, `0600`, under the
  `0700` data dir) and uses it, logging where it was written and that it must be backed up *separately*
  from the database. So **encryption is always on.** True plaintext is reachable only behind an
  explicit `secrets.allow_plaintext` opt-in that **fails closed** if unset — never the silent default.
  *(This tightens the earlier "plaintext-with-a-loud-warning" stance: a warning does not protect a
  `.db` that gets copied to a backup or pasted into a bug report.)*
- A wrong or changed key **fails loud**: a canary record is decrypt-verified at startup and harbrr
  refuses to touch secrets rather than silently dropping or re-encrypting garbage. Losing the key
  means tracker creds must be re-entered (acceptable — they are re-enterable), which is exactly why
  the login password is *hashed*, not encrypted: a lost key must never lock the admin out of their
  own UI.

### Web-UI / management-API authentication (the qui pattern)

- A **first-run setup** flow creates the single admin (argon2id password, minimum length enforced).
- **Server-side sessions** (cookie: `HttpOnly`, `Secure` behind TLS, `SameSite`), plus an
  **`X-API-Key`** header for programmatic clients and a query-param `apikey` on the *arr-facing
  Torznab feed URL. **CSRF** on cookie-authenticated mutating endpoints; the Torznab XML surface is
  apikey-authenticated and therefore CSRF-exempt.
- An **auth-disabled + trusted-proxy / IP-allowlist** mode for users behind an authenticating reverse
  proxy, and optional **OIDC** later (both qui features).

### The rest of the posture

- **File permissions.** Data dir `0700`; the database **and all SQLite side files** (`-wal`,
  `-journal`) `0600`.
- **Log & trace redaction (already built).** `internal/http` redacts secret query params,
  `Authorization`/`Cookie` headers and URL userinfo at every log/error/trace site. The settings layer
  carries each field's secret-vs-plaintext type so a diagnostic dump can never print a raw credential.
  Credentials never appear in logs, error messages, or Torznab responses — note the served
  download/magnet links **do** legitimately carry passkeys (intended output), and those are never
  *logged*.
- **Reverse-proxy assumptions.** Base-path/subfolder support; proxy headers trusted only when
  configured.
- **Safe export/import.** Config/DB export redacts secrets by default behind a `<redacted>` sentinel
  (qui-style: re-submitting the sentinel keeps the stored value); including secrets is an explicit,
  separately-passphrase-encrypted opt-in.
- **Never-store / never-log deny-list.** Login password → only the argon2id hash; API keys and
  session tokens → only a hash; decrypted tracker creds → never written except as ciphertext, never
  in the stats/event log, never in a Torznab response or *arr-facing error, never in a config export
  unless the explicit encrypted opt-in.

MVP scope note: the three-class model, encryption-always-on, redaction, file permissions, and
management-API auth land together in **Phase 4 (Daemon foundation)**; key rotation and external KMS
are deferred to later phases.

## 10. Definition lifecycle

Vendoring a corpus is not a one-time copy — it needs an operating policy:

- **Source & snapshot.** A Jackett snapshot embedded via `//go:embed`, pinned to a specific upstream
  commit recorded in the build and reported at runtime, so users know exactly what they run.
- **Update cadence.** Re-vendored from Jackett in CI on each release; optionally a scheduled CI job
  that opens a PR when upstream changes.
- **Sync process.** Automated CI fetch of the upstream definitions at the pinned ref → commit → the
  embedded snapshot ships with the binary. No runtime fetching.
- **Local overrides / drop-in.** A user drop-in directory **takes precedence** over embedded defs
  (same `id` → drop-in wins) — for the one Prowlarr-only tracker, custom defs, or hotfixes without
  waiting for a release.
- **Patch policy.** Embedded defs are **never edited in place** (byte-for-byte from Jackett); fixes go
  upstream to Jackett or ship as a local drop-in override.
- **Conflict resolution.** Precedence is drop-in > embedded; on duplicate `id`, the shadowed embedded
  def is logged.
- **Disabling broken defs.** A def that fails schema validation or parse is **skipped and recorded on
  a visible skip-list, never silently dropped**; users can also disable a def by `id`.
- **Pinning & drift.** The embedded snapshot is pinned to the binary version; individual defs are
  pinned/overridden via the drop-in dir. Drop-in defs are defensively parsed with unknown fields
  tolerated (no version negotiation — harbrr embeds lockstep like Jackett).

## 11. Autobrr family integration

This section is the spine of the project, not an appendix: it is what makes harbrr a family member
rather than a Prowlarr clone. Guiding principle: **family-native first, Prowlarr-compatible second** —
Torznab and *arr app-sync are the compatibility surface; the center of gravity is integration with
autobrr, qui, and cross-seed.

### Division of labor

| Tool | Owns | Seam with harbrr | harbrr does NOT |
|---|---|---|---|
| **autobrr** | real-time IRC announce + feed polling → filters → actions | harbrr is autobrr's Torznab/Newznab **feed + search provider** (the Prowlarr slot); shared tracker identity | …parse IRC announces |
| **qui** | qBittorrent management UI | shared `go-qbittorrent`; harbrr pushes grabs, qui manages them | …reimplement qBit management/UI |
| **mkbrr** | torrent **creation** | shares the tracker-identity layer | …create torrents |
| **upbrr** | **upload** automation | shares tracker identity + category maps | …upload |
| **rls** | release-name parsing | adopted as-is (§6) | …port Prowlarr's Parser |

### The family pipeline

```
                    ┌─ IRC announce  (live, ms latency) ───────────────→ autobrr ─┐
 trackers ──────────┤                                                   (filters   ├─→ qBittorrent ──→ qui
 (one shared id,    └─ HTTP feed/search (harbrr) ──→ autobrr Feeds ────→ + actions)│   (go-qbittorrent)   (manage/UI)
  one credential set)                                                              ├─→ *arr apps (Sonarr/Radarr/…)
                          harbrr also ──→ Torznab/Newznab + on-demand search ──────┘
                          ├─→ *arr app-sync        (Prowlarr-compat)
                          └─→ cross-seed backend   (multi-tracker search)
 upload side:  seedbox ──→ mkbrr (create) ──→ upbrr (upload spec) ──→ tracker
```

### Integration points

1. **harbrr → autobrr feeds: drop-in today, native tomorrow.** autobrr already consumes Generic
   Torznab/Newznab; point it at harbrr instead of Prowlarr and nothing else changes. The upgrade only
   the family can do: a **native push** path so newly-scraped releases reach autobrr's filters
   immediately instead of waiting on RSS polling.
2. **Shared tracker identity / registry.** One tracker `id` plus one credential entry shared between
   autobrr's IRC defs and harbrr's Cardigann defs, removing the by-hand "External identifier"
   mapping autobrr users do today.
3. **harbrr ↔ qBittorrent / qui.** Grabs push via `go-qbittorrent`; qui manages the torrents.
4. **harbrr as the cross-seed search backend.** cross-seed needs a Torznab indexer source — harbrr
   provides it natively.

### Shared tracker registry (longer-term)

Each brr tool encodes a *different facet* of the same trackers, so every new tracker is described
three or four times. The right shape is a small **shared identity layer** (tracker `id`,
names/aliases, domains, type/privacy, auth-key schema, category map) with **per-tool facet files keyed
by that `id`**, in one community repo (e.g. `autobrr/trackers`). harbrr does not block on it: vendor
Jackett now, design the loader so the search facet could later live in the registry, seed the `id`
layer over time. (Precedent: autobrr's defs descend from autodl-irssi `.tracker` files; Cardigann is
already shared across Jackett, Prowlarr, and Sonarr v3.)

## 12. Definition of done — success criteria & compatibility matrix

"Parity" is not a feeling; the engine proof is complete only when both of the following pass.

### Success criteria

- Loads **100% of vendored definitions without panic**.
- **Zero silent schema-validation failures** — every failure is triaged onto an explicit skip-list
  with a reason; nothing is silently dropped. (A vendored, known-good-in-Jackett corpus warrants a
  hard bar here, not a 95% tolerance that would hide regressions.)
- Passes the **ported Jackett Cardigann test suite**.
- Matches Jackett's normalized output on **≥25 saved-response fixtures** spanning the matrix below.
- **Sonarr/Radarr can search 5 real trackers** through Torznab (caps + results).
- **Secrets are redacted** in all logs and traces.
- **Broken indexers degrade cleanly** — skipped and surfaced, never crashing the process.

### Compatibility matrix

The matrix splits by what is honestly testable offline. **Engine parity** is gated by the offline
rows; the fetch/auth rows gate *live readiness*, not the offline proof.

**Offline (saved-fixture) archetypes — required for engine parity:**

- HTML tracker, **form** login
- HTML tracker, **cookie** login
- **JSON-API** tracker
- **XML / Newznab** tracker
- **Non-Latin-script** tracker (locale → regexp2 path)
- **Freeleech** / download-upload-volume-factor fields
- **Multi-category** tracker (category mapping)
- **Date-heavy** tracker (multiple .NET formats + relative dates)
- **Magnet-only** tracker (magnet/infohash synthesis)
- **Download-link pre-request** tracker

**Fetch/auth archetypes — gate live readiness (need live access or saved-post-challenge responses):**

- **Cloudflare / FlareSolverr**-gated tracker
- **2FA / manual-cookie** tracker

## 13. Roadmap (ordered by risk retirement) & release positioning

The project lives or dies on engine parity, so the order retires the riskiest unknowns first. Auth is
**inside** the engine proof (it is a pipeline module, §7), not a later phase:

1. **Engine proof (offline).** Parse the corpus; run the full extraction pipeline — templates,
   filters, selectors, dates, regex — *and the login/session executor* — over saved fixtures.
2. **Offline parity.** Port Jackett's Cardigann tests; add golden fixtures and saved (including
   **authenticated**) responses; differential-test against Jackett. Gated by the §12 matrix.
3. **Minimal Torznab output.** Caps/category correctness + search, so Sonarr/Radarr consume real
   results from a handful of trackers.
4. **Daemon foundation.** Persistence (SQLite + migrations), the §9 secrets store, the
   indexer-instance registry, the management API + auth/session, server wiring, and the Docker
   image/config — harbrr becomes a configurable headless daemon Sonarr/Radarr/autobrr can point at.
5. **Live smoke tests.** 5 real trackers end-to-end, including live login/session.
6. **Operational safety.** Timeouts, backoff, per-indexer rate limits (anti-blacklist); health/status.
7. **Scale coverage.** JSON/XML response modes, broader coverage, edge-case selectors/dates;
   backup/restore.
8. **Product polish.** Application sync to *arr, Jackett/Prowlarr migration, web UI, stats,
   notifications, Postgres.

Steps 1–5 are the MVP and the point the central risk is retired; everything past is productization of
a proven foundation.

### Public-release positioning

The first public release must **not** claim "Prowlarr replacement." It claims an **experimental
Cardigann-compatible Torznab provider for autobrr-family users.** The "replacement" framing waits
until application sync, migration, a UI, and broad coverage exist — which keeps expectations sane and
the project credible with maintainers. A sane adoption path: CLI conformance tool → local Torznab for
3–5 trackers → Docker image → config UI/API → autobrr feed integration → Prowlarr/Jackett import →
app sync.

## 14. Licensing

harbrr is **GPL-2.0-or-later**, matching autobrr and qui, which is compatible with **Jackett's
GPL-2.0** corpus — that compatibility is why definitions are vendored from Jackett. Note the
distinction that actually matters: Prowlarr *the application* is GPL-3.0, but that is **irrelevant**
here because harbrr uses no Prowlarr code; the operative fact is that the **`Prowlarr/Indexers`
definitions repo carries no license** (all rights reserved), so those defs are not redistributable.
The one Prowlarr-only tracker and any user/custom defs go through the local drop-in directory instead,
keeping distribution clean. (Not legal advice.)

---

## Appendix

### A. Corpus census (measured)

Across the v11 / Jackett corpora (552 Prowlarr defs, 558 Jackett defs; 549 overlap, 7 Jackett-only, 1
Prowlarr-only; near-identical engineering profile):

- **Response modes:** ~107 JSON-API, ~445 HTML-scrape (HTML is the implicit default).
- **Filter vocabulary is small:** six operations are ~95% of usage — `re_replace` (1573), `replace`
  (851), `append` (564), `dateparse` (562), `regexp` (313), `querystring` (283); ~18 in the tail.
- **Templating is trivial:** ~11,820 `{{ }}` expressions but only ~44 function calls, using two
  functions (`join`, `re_replace`); the rest is variables, `if`, `range`.
- **Regex:** ~40 / 552 (~7.2%) use constructs RE2 cannot compile (dominated by negative lookahead);
  ~190 definitions are non-Latin-script (112 `zh-CN`, 50 `ru-RU`, plus Greek/Thai/Arabic/Ukrainian),
  the population routed to regexp2 for locale-correct `\d`/`\w`/`\s`.
- **Selectors:** `:has` ~357, `:contains` ~330, `:not` ~187, `:nth-child` ~288, `:matches` 0.
- **Auth/session:** 466 / 558 (83%) log in — `form` 132, `cookie` 126, `get` 106, `post` 101; 75
  involve captcha (14 reCAPTCHA, 8 hCaptcha); 183 accept a cookie/2FA input; 61 name
  Cloudflare/DDoS-Guard.
- **Native families:** UNIT3D ~80 defs, Gazelle ~6 defs, Avistaz 0 defs.
- **Common fields:** seeders/leechers (552), size (553), freeleech via download/uploadvolumefactor
  (~550 — the most universal signal), imdbid (223), tmdbid (93).

### B. Research notes

Cardigann lineage: an original Go `cardigann` seeded the format; **Jackett** grew the corpus (GPL-2.0,
definitions bundled in-app, lockstep with its engine — hence no schema-version negotiation);
**Prowlarr** forked its own `Indexers` repo, ported the engine to .NET, and formalized v1–v11 schema
versions *because* it fetches definitions at runtime and must negotiate engine/def skew. harbrr
embeds and vendors like Jackett, so it needs no version negotiation — only defensive parse-and-skip
on drop-in defs.

### C. Review responses (audit trail)

The design was stress-tested across two adversarial reviews; resolutions are folded into the body.
Recorded here for anyone who wants the reasoning:

- **Independence vs. corpus dependence** — independent in product/engine/API; deliberately dependent
  on the Cardigann *community* corpus (two corpora exist), not Prowlarr.
- **Corpus licensing** — vendor Jackett's GPL-2.0 corpus, not Prowlarr's unlicensed one.
- **HTML-selector parity** — cascadia supports the vocabulary; residual edge cases covered by a
  standing selector fixture suite.
- **Validation oracle** — Jackett-engine parity, offline; live-def correctness is the corpus's job.
- **Native-family build order** — corpus covers UNIT3D/Gazelle; Avistaz (0 defs) is the gap, made a
  post-parity milestone.
- **Regex behavioral gap** — RE2 silently differs from .NET on `\d`/`\w`/`\s` over non-Latin text;
  resolved by locale-aware regexp2 routing, with the rule kept implementable (no undecidable
  "provably equivalent" bar) and ReDoS safety as the reason RE2 stays default.
- **Anti-bot upkeep** — delegate to FlareSolverr behind a pluggable interface.
- **Engine-parity optimism** — reframed: the quirks are matched (differential vs Jackett), not
  enumerated; success is pinned by an explicit compatibility matrix and success criteria (§12).
- **Auth ordering** — auth is a pipeline module inside the engine proof, not later product polish.
- **Security & definition lifecycle** — added as first-class sections (§9, §10).
- **Effort vs. payoff** — acknowledged; pursued as a narrow, test-harness-first engine proof.
