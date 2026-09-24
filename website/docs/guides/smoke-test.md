# Golden smoke test (`harbrr smoke`)

`harbrr smoke` is a built-in, operator-run health check: it drives your **live** harbrr stack
and grades it against Prowlarr and your *arr/qui apps, then writes a **secret-scrubbed report**
you can paste straight into a GitHub issue. It ships in the binary, so it runs natively or
inside the container — no Go toolchain, no separate download.

Use it to confirm an upgrade is healthy, to reproduce a problem before reporting it, or as a
pre-release gate.

---

## What it checks

For every indexer configured in harbrr, in one run:

- **Parity vs Prowlarr** — searches the same query on harbrr and on Prowlarr (matched by indexer
  name) and compares the result sets within tolerance (count ratio + title overlap). An indexer
  that isn't configured in Prowlarr is reported **not-comparable**, never a failure.
- **App-sync** — that harbrr's indexers are present and correct in Sonarr/Radarr/qui: the
  content-category filter holds (e.g. a books/audiobook tracker is **not** pushed to Radarr or
  Sonarr), each feed URL is the current `/api/indexers/{slug}/results/torznab` path (not the old
  `/api/v2.0/…`) and returns `200`, and qui uses the `/full` freeleech-bypass variant.
- **Field parity** — `size`, `category` (the major Torznab bucket), and the download-link shape
  are compared on every run. `seeders` and `publishDate` move between the two fetches, so they
  are only compared under `SMOKE_STRICT_FIELDS=1`.
- **Cache** — a repeated identical search is served from cache (the tracker isn't hit twice).
- **FL-bypass** — qui receives the full-catalog `/full` feed.

It **never grabs by default** (no hit-and-run). `SMOKE_GRAB=1` opts into a per-tracker real
grab check, and even then nothing is pushed to a download client.

---

## Running it

Native:

```bash
harbrr smoke
```

In Docker (the command ships in the image), the env file has to be inside the container, so
copy it in and point `--env-file` at it:

```bash
docker cp smoke.env <harbrr-container>:/config/smoke.env
docker exec <harbrr-container> harbrr smoke --env-file /config/smoke.env
```

The run prints a summary and writes `smoke-report.md` in the working directory. It exits
**non-zero** if anything failed, so it scripts cleanly in CI-of-your-own or a cron.

---

## Configuration

The command is non-interactive: it reads its config from `./smoke.env` (point elsewhere with
`--env-file`) or from the real environment, which takes precedence over the file. When a required
variable is missing it prints this template and exits non-zero — create the file by hand at mode
`0600` (`smoke.env` is gitignored; the keys are secret):

```bash
# harbrr smoke config — keys are secret; do not commit. Write at mode 0600.
export SMOKE_HARBRR_URL=http://harbrr:7478
export SMOKE_HARBRR_APIKEY=
export SMOKE_PROWLARR_URL=http://prowlarr:9696
export SMOKE_PROWLARR_APIKEY=
#export SMOKE_SONARR_URL=
#export SMOKE_SONARR_APIKEY=
#export SMOKE_RADARR_URL=
#export SMOKE_RADARR_APIKEY=
#export SMOKE_QUI_URL=
#export SMOKE_QUI_APIKEY=
```

harbrr and Prowlarr are required; Sonarr/Radarr/qui are optional — leave an app's lines commented
out to skip its checks. Optional knobs:

```text
SMOKE_QUERY, SMOKE_QUERY_FALLBACK          # optional — force one query for every tracker
SMOKE_GRAB=1                               # optional — add the per-tracker grab check
SMOKE_STRICT_FIELDS=1                      # optional — also compare seeders and publishDate
```

---

## Reading & sharing the report

`smoke-report.md` is **failures-first**: a summary, then a Failures section (the part worth
pasting into an issue), then not-comparable items, then a collapsible full table. It is
**secret-safe by construction** — every URL and error is run through harbrr's redactors and the
whole document is scanned for credential tokens before it's written, so no API key, passkey, or
feed secret ever lands in it. It's safe to attach to a public GitHub issue as-is.

---

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--env-file` | `./smoke.env` | Path to the `export SMOKE_*=…` env file |
| `--report` | `./smoke-report.md` | Where to write the markdown report |
| `--query` | *(category-derived)* | Force one search query for every tracker (overrides `SMOKE_QUERY`) |
| `--fallback-query` | *(category-derived)* | Query tried when the first returns nothing (overrides `SMOKE_QUERY_FALLBACK`) |

Left unset, the queries are **derived per indexer** from the categories it advertises, so each
side returns a small, comparable set instead of slamming the 100-result page cap: Movies gets
`Oppenheimer 2023` (fallback `Dune 2021`), TV `The Last of Us S01E01`, Audio `Radiohead In
Rainbows`, Books `Project Hail Mary`, PC `Adobe Photoshop`, Console `God of War`. A general
tracker takes the first of those its categories match; one with no recognized content category
falls back to the Movies pair.

---

## Notes

- The command reaches real trackers (through harbrr) and your *arr/Prowlarr/qui — it is
  **operator-run only** and refuses to run when `CI` is set.
- For the deeper developer differential (adds trackers with live per-tracker credentials), see
  the build-tagged `make smoke-test` harness in `docs/smoke-setup.md`.
