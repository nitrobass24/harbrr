# External issues

Bugs / limitations in **other apps** (the *arr family, qui, FlareSolverr, …) that
affect harbrr's integration but must be fixed **upstream**, not in harbrr. harbrr
works around them where it can; this log tracks the underlying issue, the workaround,
and whether it's worth reporting upstream.

Format per entry: **App · short title** — what's wrong, how we found it, harbrr's
workaround (if any), and disposition (`[Workaround shipped]`, `[Report upstream]`,
`[Accepted]`).

---

## autobrr/qui

### qui · `native` caps-sync fails for a full Torznab feed URL — `[Workaround shipped]`
- **Symptom:** in qui, harbrr-synced indexers show no capabilities, and `POST
  /api/torznab/indexers/{id}/caps/sync` returns **HTTP 500 "Failed to sync caps"**.
- **Cause:** qui's `native` backend fetches caps via its go-jackett client, which
  appears to construct the caps request from a Jackett-style base (it appends its
  own path), so a harbrr feed URL that already ends in `/results/torznab` is
  malformed. (Verified 2026-06-20 against qui at the `native`/`prowlarr` backends:
  every `prowlarr`-backend indexer had capabilities; every `native` one had none,
  and a manual caps-sync 500'd.)
- **harbrr workaround:** harbrr now **pushes** `capabilities` + `categories` in the
  create/update body (qui's `CreateIndexer`/`UpdateIndexer` store them directly via
  `SetCapabilities`/`SetCategories`), so caps populate without relying on qui's
  native sync. See `internal/torznab.CapabilityTokens` and the qui driver.
- **Upstream fix:** qui's `native` caps fetch should use the feed URL as-is (it's a
  complete Torznab endpoint) rather than reconstructing a Jackett path. `[Report
  upstream]` — the push workaround makes it non-blocking.

### qui · no first-class "harbrr" backend — `[Accepted]`
- qui's torznab indexer backends are exactly `jackett` / `prowlarr` / `native`
  (`internal/models/torznab_indexer.go`). harbrr is a direct Torznab endpoint, so
  it is synced as `native` and shows as "native" in qui — there is no way to label
  it "harbrr". Purely cosmetic. A first-class harbrr backend would be a qui feature
  request. `[Accepted]` — `native` is semantically correct.

### qui · shows indexers by numeric ID instead of name in some views — `[Report upstream]`
- **Symptom:** in some qui screens/logs a harbrr-synced indexer appears as its numeric
  qui id (e.g. `47`) rather than its name (e.g. `AURA4K`).
- **Not a harbrr bug — verified 2026-07-02.** harbrr's qui driver sends the human name in
  the create/update body (`internal/appsync/qui.go` → `buildIndexer`, `Name: d.Name`), and
  qui stores and returns it: `GET /api/torznab/indexers` on the live stack lists every
  harbrr-synced indexer with the correct `name` (`AURA4K`, `FileList`, `IPTorrents`, …). So
  the name is present in qui's own data.
- **Cause (qui side):** qui tracks results/activity by the integer `IndexerID` in several
  subsystems (`internal/models/torznab_cache.go`, `internal/services/dirscan/search.go`,
  cross-seed, and the frontend `web/src/types/{indexers,crossseed}.ts`) and does not always
  join that id back to the stored `name` when rendering, so the id shows through.
- **harbrr action:** none — harbrr already provides the name. `[Report upstream]`: qui should
  resolve `IndexerID` → indexer `name` in those display paths.
