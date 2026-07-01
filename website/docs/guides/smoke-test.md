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
- **Cache** — a repeated identical search is served from cache (the tracker isn't hit twice).
- **FL-bypass** — qui receives the full-catalog `/full` feed.

It **never grabs** (no hit-and-run) and never touches a download client.

---

## Running it

Native:

```bash
harbrr smoke
```

In Docker (the command ships in the image):

```bash
docker exec -it <harbrr-container> harbrr smoke
```

> Use `-it` on first run so the interactive prompts work. For a non-interactive/scheduled run,
> pre-populate the env file (below) and drop `-it`.

The run prints a summary and writes `smoke-report.md` in the working directory. It exits
**non-zero** if anything failed, so it scripts cleanly in CI-of-your-own or a cron.

---

## First run — one-time setup

The first run (or `--reconfigure`) prompts you **one at a time** for each app's URL and API key:
harbrr, Prowlarr, Sonarr, Radarr, qui. URLs echo; **API keys are read without echoing** to your
terminal. Sonarr/Radarr/qui are optional — leave a URL blank to skip that app's checks.

Your answers are saved to a gitignored `smoke.env` (mode `0600`) as `export SMOKE_*="…"`, so
subsequent runs are non-interactive. Re-run the setup any time with:

```bash
harbrr smoke --reconfigure
```

You can also set the values as environment variables instead of the file (the real environment
takes precedence over `smoke.env`):

```
SMOKE_HARBRR_URL, SMOKE_HARBRR_APIKEY
SMOKE_PROWLARR_URL, SMOKE_PROWLARR_APIKEY
SMOKE_SONARR_URL, SMOKE_SONARR_APIKEY      # optional
SMOKE_RADARR_URL, SMOKE_RADARR_APIKEY      # optional
SMOKE_QUI_URL, SMOKE_QUI_APIKEY            # optional
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
| `--reconfigure` | | Re-prompt for every app URL/key and rewrite the env file |
| `--env-file` | `./smoke.env` | Path to the `export SMOKE_*=…` env file |
| `--report` | `./smoke-report.md` | Where to write the markdown report |
| `--query` | `test` | Search query used for parity |
| `--fallback-query` | `2024` | Query tried when the first returns nothing |

---

## Notes

- The command reaches real trackers (through harbrr) and your *arr/Prowlarr/qui — it is
  **operator-run only** and refuses to run when `CI` is set.
- For the deeper developer differential (adds trackers with live per-tracker credentials), see
  the build-tagged `make smoke-test` harness in `docs/smoke-setup.md`.
