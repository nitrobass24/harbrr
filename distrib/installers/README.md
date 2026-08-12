# Seedbox installers

One-shot installer scripts for shared seedbox providers, ported from the qui
installers. Each script installs harbrr into the account's home directory, picks a
free port, writes `<data-dir>/config.toml`, wires the provider's reverse proxy (where
the provider has one) and its service mechanism (systemd user unit, cron + `screen`,
or start/stop scripts), then prints the URL to open.

Hosting for these is **pending** — the intended one-liners are:

| Provider | One-liner (URL pending) |
| --- | --- |
| Feral Hosting | `bash <(curl -sL https://get.autobrr.com/harbrr/feral)` |
| Seedhost | `bash <(curl -sL https://get.autobrr.com/harbrr/seedhost)` |
| Ultra.cc | `bash <(curl -sL https://get.autobrr.com/harbrr/ultra)` |
| Whatbox | `bash <(curl -sL https://get.autobrr.com/harbrr/whatbox)` |
| Hosting by Design | `bash <(curl -sL https://get.autobrr.com/harbrr/hostingbydesign)` |
| Bytesized (untested) | `bash <(curl -sL https://get.autobrr.com/harbrr/bytesized)` |

Until those URLs exist, run a script straight from a checkout, e.g.
`bash distrib/installers/feral.sh`.

## Prerequisite

The scripts resolve the download from
`https://api.github.com/repos/autobrr/harbrr/releases/latest` **unauthenticated**, and
grep the release assets for the `linux_x86_64` `.tar.gz` archive
(`harbrr_<version>_linux_x86_64.tar.gz`, per `.goreleaser.yml`). They therefore only
work once the repository is public and has a published release — before that they exit
with "Failed to query GitHub for latest version" (request failed) or "No linux_x86_64
.tar.gz asset in the latest release of autobrr/harbrr" (no matching asset).

## What every script does

- Downloads and extracts the latest linux/amd64 release archive into the provider's
  usual binary directory.
- Picks a free port (from `ss` output, or from Ultra's `app-ports` picker).
- Writes `config.toml` into harbrr's data directory with `[server] host`, `port`, and
  `base_url` set to the subpath the provider serves harbrr at (`[log] level` too).
  harbrr reads `<data-dir>/config.toml`, so the service is started with
  `harbrr serve --data-dir=<data-dir>`.
- Wires the provider's service mechanism and, where the provider exposes one, its
  nginx subpath config.
- Offers install / update / uninstall / backup. Updates re-download the latest
  release archive (harbrr has no self-updater) after backing up the data directory.

The admin account is **not** created by the script: harbrr's first-run setup happens in
the web UI, so each script ends by pointing at the URL where you create it. Nothing
secret is generated, echoed, or written by these scripts — harbrr manages its own
at-rest encryption key inside the data directory.

## Per-provider differences

| Provider | Binary | Data dir | Serving | Service |
| --- | --- | --- | --- | --- |
| feral | `~/bin` | `~/.config/harbrr` | own nginx (`~/.nginx/conf.d/000-default-server.d/harbrr.conf`), `base_url = "/<user>/harbrr"`, app bound to `10.0.0.1` | `screen` + `*/5` cron watchdog |
| seedhost | `~/bin` | `~/.config/harbrr` | direct `host:port`, `base_url = "/harbrr"` | `screen` + cron watchdog in `~/software/cron` |
| ultra | `~/bin` | `~/.apps/harbrr` | nginx `~/.apps/nginx/proxy.d/harbrr.conf`, `base_url = "/harbrr"`, bound to `127.0.0.1`; port chosen via `app-ports` | systemd user unit `harbrr.service` |
| whatbox | `~/.local/bin` | `~/.config/harbrr` | Whatbox subdomain (`harbrr.<host>`), so harbrr stays at the **root** (no `base_url`); you add the port + app name in the Whatbox panel | `screen` + user-managed crontab entries |
| hostingbydesign | `~/bin` | `~/.config/harbrr` | direct `host:port`, `base_url = "/harbrr"` | systemd user unit (`Type=exec` on systemd >= 240) |
| bytesized | `~/apps/harbrr` | `~/.config/harbrr` | direct `host:port`, `base_url = "/harbrr"` | `~/.startup` / `~/.shutdown` scripts driving `start-stop-daemon` |

The two systemd providers install a **user** unit (`WantedBy=default.target`). A user
manager only survives logout — and therefore only starts the unit at boot — when
lingering is enabled for the account. Ultra enables it for you; on Hosting by Design, if
harbrr does not come back after a reboot, run `loginctl enable-linger "$USER"` (or ask
support to).

## Caveats

- **Bytesized is untested.** It is carried over from the upstream qui installer, which
  is likewise marked untested; no account was available to validate it. The script
  prints the same warning when run.
- The other five are faithful conversions of the upstream qui scripts but have not yet
  been run on a live account — first validation happens on real provider accounts.
- Every script is shellcheck-clean (`shellcheck distrib/installers/*.sh`).
