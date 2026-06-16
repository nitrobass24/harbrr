#!/usr/bin/env bash
#
# prowlarr-extract-creds.sh — dump tracker credentials from a Prowlarr SQLite DB,
# for Phase 9 credential intake into harbrr.
#
# WHY THE DB (not the API): Prowlarr stores indexer settings — apikey / passkey /
# cookie / username / password — in PLAINTEXT in the Indexers.Settings JSON column.
# Its REST API masks those fields with "********" (SchemaBuilder), so the database is
# the only place to read them back.
#
# THE OUTPUT CONTAINS PLAINTEXT SECRETS. It prints to your terminal only — do not
# paste it into chat, commit it, or log it. Map the fields into harbrr via
# POST /api/indexers (encrypted at rest) or the Phase 9 smoke harness
# (SMOKE_SETTINGS_<SLUG>). For each tracker you want, you need: the harbrr
# definitionId (printed below) and its credential field(s).
#
# Usage:
#   scripts/prowlarr-extract-creds.sh /path/to/prowlarr.db
#
# Tip: if Prowlarr is running, copy the DB first (a hot read can error):
#   cp /path/to/config/prowlarr.db /tmp/prowlarr.db
#   scripts/prowlarr-extract-creds.sh /tmp/prowlarr.db

set -euo pipefail

db="${1:-}"
if [[ -z "$db" || ! -f "$db" ]]; then
  echo "usage: $0 /path/to/prowlarr.db" >&2
  exit 2
fi
command -v sqlite3 >/dev/null || {
  echo "error: sqlite3 not found (install it, e.g. 'apt install sqlite3' / 'brew install sqlite')" >&2
  exit 1
}

echo "# Prowlarr indexers in $db"
echo "# definitionId -> harbrr defId; credentials live in the Settings JSON (often under extraFieldData)."
echo

if command -v jq >/dev/null; then
  sqlite3 -json "$db" "SELECT Name, Implementation, Settings FROM Indexers ORDER BY Name;" \
    | jq -r '
        .[]
        | "### \(.Name)   (implementation: \(.Implementation))",
          "    harbrr definitionId: \((.Settings | fromjson).definitionFile // "— non-Cardigann (e.g. AvistaZ); map by hand")",
          "    settings (plaintext credentials inline):",
          ((.Settings | fromjson) | to_entries | map("      \(.key): \(.value | tojson)") | .[]),
          ""'
else
  echo "(jq not found — printing raw Settings JSON per indexer; install jq for a cleaner view)" >&2
  echo
  sqlite3 "$db" "SELECT '### ' || Name || '   [' || Implementation || ']' || char(10) || Settings || char(10) FROM Indexers ORDER BY Name;"
fi
