# harbrr

harbrr is the autobrr family's Torznab/Newznab search provider: it runs the community's
Cardigann tracker definitions through a parity-gated engine and serves the results to the
*arr apps. This glossary is the project's ubiquitous language; architectural design records
live in `docs/architecture.md`.

## Language

### Trackers and definitions

**Definition**:
A declarative Cardigann YAML adapter for one tracker, consumed byte-for-byte from the
vendored Jackett snapshot or a local drop-in.
_Avoid_: config, template, indexer file

**Native driver**:
A Go-built driver for a tracker whose contract exceeds the declarative Cardigann format;
it satisfies the same engine-shaped core the Cardigann engine does.
_Avoid_: custom indexer, plugin

**Family**:
The unit a native driver covers — one API shape, possibly many sites (one Gazelle driver
serves Redacted and Orpheus).
_Avoid_: site, tracker (when the shape is meant)

### Native driver anatomy

**Driver base**:
The shared implementation core every native driver embeds: instance wiring, transport,
redaction, and status classification. A driver adds only its request generator and
response parser.
_Avoid_: base class, common helpers, utils

**Request generator**:
The per-family piece that turns a search query into an authenticated tracker request
(Prowlarr's split).
_Avoid_: query builder

**Response parser**:
The per-family piece that turns a tracker response body into normalized releases
(Prowlarr's split).
_Avoid_: scraper

**Classification dialect**:
An endpoint's mapping from HTTP statuses to meaning — which codes say "credentials bad"
versus "back off" (403 is a spent rate budget on HDBits, an expired session on
MyAnonamouse).
_Avoid_: error mapping, status handling

### Serving

**Indexer**:
A searchable tracker as the rest of harbrr consumes it — identity, capabilities,
and Search/Grab — independent of whether a Cardigann Definition or a native driver
backs it. The `core.Indexer` contract (`internal/indexer/core`) is satisfied by an
adapter over the engine (or a native driver) and served by the Torznab/Newznab
handler.
_Avoid_: tracker (when the served contract is meant), provider (that is the
id → Indexer lookup).

**Grab**:
The server-side fetch of one release's torrent/NZB that the `/dl` proxy drives, so
credentials and passkey-bearing links never reach the *arr client.
_Avoid_: download (for the server-side fetch), snatch

**Rotation**:
A tracker refreshing a session credential on every response; the driver must capture and
persist the new value or the session dies (MyAnonamouse's `mam_id`).
_Avoid_: refresh, renewal

### Process shape

**Composition root**:
`internal/app`, the single place that builds the dependency graph (in a fixed
construction order) and owns process lifecycle (`Run`) and the full-daemon test
handler (`Handler`). `cmd/harbrr` only parses flags and calls it; `internal/server`
only mounts HTTP handlers onto a listener.
_Avoid_: bootstrap, wiring (as a package name), main package logic

### Connections

**Connection resource**:
One of harbrr's three encrypted-secret connection types (an app-sync connection, an
announce connection, a notification target): a row plus one or more secrets sealed at
rest under the row's own id, whose create/update/delete lifecycle — including an
optional minted-key mint and fail-closed revoke — is sequenced by
`internal/connresource.Lifecycle[T]`. `Lifecycle[T]` also sequences Create and Update
(not Delete) for two encrypted-secret **infra resources** — proxy and solver — but those
are not connections: each is a transport or anti-bot endpoint an indexer instance points
at, not a harbrr-to-remote-service link, and neither mints a key. "Connection resource"
stays the term for appsync/announce/notify specifically; **encrypted-secret resource** is
the broader umbrella covering all five.
_Avoid_: entity (too generic), integration (a different word already used for the app-sync
targets themselves)

**Bind**:
Resolving a surface row's App into usable form — the App plus its decrypted credential —
for driver construction (`apps.Service.Bind`). The display half of the same App-identity
projection is Enrich (`apps.EnrichList`/`apps.EnrichOne`): list tolerates a dangling App
reference (keeps the row's stored fields), get errors.
_Avoid_: hydrate, resolve (that is Ref → App adoption, `apps.Service.Resolve`)
