#!/usr/bin/env bash
#
# phase9-smoke.sh — one repeatable command for the Phase 9 live pass: pull every
# tracker's credentials out of Prowlarr, build the harness env, and run the smoke
# harness against all of them. No tracker-picking, no typing creds/cookies.
#
# Two modes:
#   EXTRACT (default) — read creds from prowlarr.db, run the harness. By default the
#     creds are eval'd into this process only and discarded (never touch disk). Set
#     SMOKE_ENV_FILE to ALSO save a complete, sourceable env bundle locally so you can
#     re-run later (e.g. after Phase 9.5 ships) without re-extracting. That file holds
#     LIVE secrets — it is written mode 600 and is gitignored (.env.phase9 / *.smoke.env);
#     never commit or share it.
#   REUSE — SMOKE_REUSE_ENV=1 + SMOKE_ENV_FILE=<saved file> sources the saved bundle and
#     runs the harness directly. No prowlarr.db needed. This is the "re-run the same live
#     tests later" path.
#
# The harness itself only ever writes secret-free evidence to internal/smoke/testdata/.
#
# Required env (EXTRACT mode):
#   SMOKE_HARBRR_URL      e.g. http://127.0.0.1:7474
#   SMOKE_HARBRR_APIKEY   a harbrr API key (POST /api/apikeys)
#   SMOKE_PROWLARR_URL    e.g. http://prowlarr:9696
#   PROWLARR_DB           path to prowlarr.db (copy it first if Prowlarr is running)
# Optional:
#   SMOKE_ENV_FILE        save the full env bundle here for repeatable re-runs (gitignored)
#   SMOKE_REUSE_ENV=1     skip extraction; source SMOKE_ENV_FILE and run
#   SMOKE_PROWLARR_APIKEY (auto-extracted from PROWLARR_DB when unset)
#   SMOKE_QUERY / SMOKE_QUERY_FALLBACK / SMOKE_GRAB (see the harness doc comment)
#
# Usage (extract + save for later):
#   SMOKE_HARBRR_URL=… SMOKE_HARBRR_APIKEY=… SMOKE_PROWLARR_URL=… PROWLARR_DB=/tmp/prowlarr.db \
#     SMOKE_ENV_FILE=.env.phase9 scripts/phase9-smoke.sh
# Usage (re-run later from the saved bundle):
#   SMOKE_REUSE_ENV=1 SMOKE_ENV_FILE=.env.phase9 scripts/phase9-smoke.sh

set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${SMOKE_ENV_FILE:-}"

mode="extract"
if [[ "${SMOKE_REUSE_ENV:-0}" == "1" ]]; then
  # REUSE mode — source the saved bundle, skip extraction.
  mode="reuse"
  [[ -n "$ENV_FILE" ]] || { echo "phase9-smoke: SMOKE_REUSE_ENV=1 needs SMOKE_ENV_FILE=<saved file>" >&2; exit 1; }
  [[ -f "$ENV_FILE" ]] || { echo "phase9-smoke: SMOKE_ENV_FILE '$ENV_FILE' not found" >&2; exit 1; }
  echo "phase9-smoke: reusing saved env from $ENV_FILE (no extraction)" >&2
  # shellcheck disable=SC1090 # ENV_FILE is an operator-supplied path, not a fixed source.
  set -a; . "$ENV_FILE"; set +a
else
  # EXTRACT mode — pull creds from prowlarr.db.
  : "${PROWLARR_DB:?set PROWLARR_DB=/path/to/prowlarr.db}"
  # Pull SMOKE_PROWLARR_APIKEY + SMOKE_TRACKERS + every SMOKE_SETTINGS_<SLUG> from the DB.
  creds="$("$repo/scripts/prowlarr-extract-creds.sh" --env "$PROWLARR_DB")"
  eval "$creds"
fi

# Required in BOTH modes (a reuse bundle must carry these too) — validate before use.
: "${SMOKE_HARBRR_URL:?set SMOKE_HARBRR_URL=http://host:7474}"
: "${SMOKE_HARBRR_APIKEY:?set SMOKE_HARBRR_APIKEY=<a harbrr API key>}"
: "${SMOKE_PROWLARR_URL:?set SMOKE_PROWLARR_URL=http://host:9696}"

# Save a complete, sourceable bundle for re-runs (extract mode only, after validation).
# Operator vars are written LAST so they win over any stale DB-extracted value.
if [[ "$mode" == "extract" && -n "$ENV_FILE" ]]; then
  ( umask 077
    {
      echo "# phase9-smoke env bundle — LIVE SECRETS. Machine-local, gitignored, never commit/share."
      echo "# Re-run later: SMOKE_REUSE_ENV=1 SMOKE_ENV_FILE=$ENV_FILE scripts/phase9-smoke.sh"
      printf '%s\n' "$creds"
      echo "export SMOKE_HARBRR_URL=$(printf '%q' "$SMOKE_HARBRR_URL")"
      echo "export SMOKE_HARBRR_APIKEY=$(printf '%q' "$SMOKE_HARBRR_APIKEY")"
      echo "export SMOKE_PROWLARR_URL=$(printf '%q' "$SMOKE_PROWLARR_URL")"
      echo "export SMOKE_PROWLARR_APIKEY=$(printf '%q' "${SMOKE_PROWLARR_APIKEY:-}")"
    } > "$ENV_FILE" )
  chmod 600 "$ENV_FILE"
  case "$ENV_FILE" in
    .env.phase9 | *.smoke.env | */.env.phase9 | */*.smoke.env) ;;
    *) echo "phase9-smoke: WARNING — $ENV_FILE may not be gitignored; verify with 'git check-ignore $ENV_FILE'" >&2 ;;
  esac
  echo "phase9-smoke: saved env bundle -> $ENV_FILE (mode 600, gitignored)" >&2
fi

if [[ -z "${SMOKE_TRACKERS:-}" ]]; then
  echo "phase9-smoke: no indexers in env (extract from prowlarr.db, or check SMOKE_ENV_FILE)" >&2
  exit 1
fi
if [[ -z "${SMOKE_PROWLARR_APIKEY:-}" ]]; then
  echo "phase9-smoke: SMOKE_PROWLARR_APIKEY is unset and not in the DB (newer Prowlarr keeps it" >&2
  echo "  in config.xml). Set it: Prowlarr -> Settings -> General -> API Key." >&2
  exit 1
fi

# Print the (non-secret) tracker mapping only — never the SMOKE_SETTINGS_* values.
echo "phase9-smoke: running against:" >&2
tr ',' '\n' <<<"$SMOKE_TRACKERS" | sed 's/^/  - /' >&2

cd "$repo"
exec go test -tags smoke ./internal/smoke/ -run TestSmoke -v
