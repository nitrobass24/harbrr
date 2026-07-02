#!/usr/bin/env bash
#
# prowlarr-extract-creds.sh — read tracker credentials out of a Prowlarr SQLite DB.
#
# WHY THE DB (not the API): Prowlarr stores indexer settings — apikey / passkey /
# cookie / username / password / pid — in PLAINTEXT in the Indexers.Settings JSON
# column. Its REST API masks those fields with "********", so the database is the only
# place to read them back. harbrr and Prowlarr consume the SAME Cardigann definitions,
# so a Cardigann indexer's stored field values map 1:1 onto harbrr's settings.
#
# THE OUTPUT CONTAINS PLAINTEXT SECRETS. Keep it on your machine — do not paste it into
# chat, commit it, or log it.
#
# Modes:
#   (default)   human-readable dump for inspection (definitionId + settings per tracker)
#   --env       machine-readable `export SMOKE_*` lines for the Phase 9 smoke harness:
#               SMOKE_PROWLARR_APIKEY (from the DB), SMOKE_TRACKERS, and one
#               SMOKE_SETTINGS_<SLUG> per indexer. Shell-safe (jq @sh), so:
#                   eval "$(scripts/prowlarr-extract-creds.sh --env prowlarr.db)"
#               or use scripts/phase9-smoke.sh, which wraps the whole run.
#
# Usage:
#   scripts/prowlarr-extract-creds.sh [--env] /path/to/prowlarr.db
#
# Tip: if Prowlarr is running, copy the DB first (a hot read can error):
#   cp /path/to/config/prowlarr.db /tmp/prowlarr.db

set -euo pipefail

mode="human"
if [[ "${1:-}" == "--env" ]]; then
  mode="env"
  shift
fi

db="${1:-}"
if [[ -z "$db" || ! -f "$db" ]]; then
  echo "usage: $0 [--env] /path/to/prowlarr.db" >&2
  exit 2
fi
command -v sqlite3 >/dev/null || {
  echo "error: sqlite3 not found (e.g. 'apt install sqlite3' / 'brew install sqlite')" >&2
  exit 1
}
command -v jq >/dev/null || {
  echo "error: jq not found (e.g. 'apt install jq' / 'brew install jq')" >&2
  exit 1
}

if [[ "$mode" == "env" ]]; then
  # Prowlarr's own API key (the differential oracle): older builds keep it in the Config
  # table, newer ones in config.xml (NOT the DB). Emit it if present; otherwise the
  # operator supplies SMOKE_PROWLARR_APIKEY (Prowlarr -> Settings -> General -> API Key).
  pk="$(sqlite3 "$db" "SELECT Value FROM Config WHERE Key='ApiKey' LIMIT 1;" 2>/dev/null || true)"
  [[ -n "$pk" ]] && printf 'export SMOKE_PROWLARR_APIKEY=%q\n' "$pk"

  # Indexer proxies (FlareSolverr / HTTP / SOCKS) live in a SEPARATE table, applied to
  # an indexer when they share a tag (a proxy with no tags applies to all). Read them so
  # a Cloudflare/proxy tracker gets solver_type/flaresolverr_url or proxy_type/proxy_url
  # injected automatically. A missing table -> [].
  proxies="$(sqlite3 -json "$db" "SELECT Implementation, Settings, Tags FROM IndexerProxies;" 2>/dev/null || true)"
  [[ -z "$proxies" ]] && proxies='[]'

  # One SMOKE_SETTINGS_<SLUG> per indexer + a combined SMOKE_TRACKERS, derived straight
  # from the DB. Cardigann creds live in extraFieldData; native (AvistaZ family) creds at
  # the top level; proxy/solver config comes from the matched IndexerProxies. Only
  # string-valued fields are kept (drops checkboxes/numbers); structural keys are removed.
  sqlite3 -json "$db" "SELECT Name, Implementation, Settings, Tags FROM Indexers ORDER BY Name;" \
    | jq -r --argjson proxies "$proxies" '
        def envname: ascii_upcase | gsub("[^A-Z0-9]"; "_");
        def strfields(o): (o // {}) | with_entries(select((.value|type)=="string" and (.value|length)>0));
        def tagset(t): (t // "[]") | (try fromjson catch []);
        def proxyURL($scheme; $ps):
          $scheme + "://"
          + (if (($ps.username // "") != "") then
               $ps.username + (if (($ps.password // "") != "") then ":" + $ps.password else "" end) + "@"
             else "" end)
          + ($ps.host // "")
          + (if ($ps.port != null) then ":" + ($ps.port | tostring) else "" end);
        # proxy/solver settings to inject for an indexer, by tag overlap with each proxy.
        def proxyFields($itags):
          reduce $proxies[] as $p ({};
            (tagset($p.Tags)) as $ptags
            | if (($ptags | length) == 0) or (any($ptags[]; . as $x | ($itags | index($x)) != null)) then
                ($p.Settings | try fromjson catch {}) as $ps
                | ($p.Implementation | ascii_downcase) as $impl
                | if $impl == "flaresolverr" then
                    . + {solver_type: "flaresolverr", flaresolverr_url: ($ps.host // $ps.url // "")}
                  elif ($impl == "http" or $impl == "https") then
                    . + {proxy_type: "http", proxy_url: proxyURL("http"; $ps)}
                  elif $impl == "socks5" then
                    . + {proxy_type: "socks5", proxy_url: proxyURL("socks5"; $ps)}
                  else .  # socks4 is unsupported by harbrr -> skipped
                  end
              else . end);
        def patt(f): if f.solver_type == "flaresolverr" then "cloudflare"
                     elif f.proxy_type then "proxy"
                     elif f.apikey then "apikey"
                     elif f.cookie then "cookie"
                     elif f.pid then "avistaz"
                     elif (f.username and f.password) then "form"
                     else "generic" end;

        [ .[]
          | (.Settings | try fromjson catch {}) as $s
          | ( $s.definitionFile
              // ( (.Implementation | ascii_downcase) as $impl
                   | if ($impl | test("avistaz|cinemaz|privatehd|exoticaz|iptorrents|myanonamouse|filelist")) then $impl else null end )
            ) as $def
          | select($def != null)
          | ( (strfields($s.extraFieldData) + strfields($s))
              | del(.definitionFile, .baseUrl, .torznabView, .baseSettings)
              | with_entries(select(.key | startswith("info") | not))
              # Native C# settings use camelCase; the harbrr native drivers use
              # snake_case. Best-effort remap of the known fields (verify against the
              # live DB if a native add fails): mamId->mam_id, userAgent->user_agent.
              | with_entries(.key |= ({"mamId": "mam_id", "userAgent": "user_agent"}[.] // .)) ) as $base
          | (proxyFields(tagset(.Tags))
             | with_entries(select((.value|type)=="string" and (.value|length)>0))) as $prox
          | ($base + $prox) as $fields
          | select(($fields | length) > 0)
          | { slug: ($def | gsub("[^a-zA-Z0-9._-]"; "-")), def: $def, fields: $fields }
        ] as $idx
        | ( "export SMOKE_TRACKERS="
            + ( ($idx | map("\(.slug)|\(.def)|\(.def)|\(patt(.fields))") | join(",")) | @sh ) ),
          ( $idx[] | "export SMOKE_SETTINGS_\(.slug | envname)=" + (.fields | tojson | @sh) )'
  exit 0
fi

# --- human-readable mode -----------------------------------------------------
echo "# Prowlarr indexers in $db"
echo "# definitionId -> harbrr defId; credentials are in the Settings JSON (Cardigann: under extraFieldData)."
echo
sqlite3 -json "$db" "SELECT Name, Implementation, Settings FROM Indexers ORDER BY Name;" \
  | jq -r '
      .[]
      | "### \(.Name)   (implementation: \(.Implementation))",
        "    harbrr definitionId: \((try (.Settings | fromjson).definitionFile catch null) // "— non-Cardigann (e.g. AvistaZ); map by hand")",
        "    settings (plaintext credentials inline):",
        ((try (.Settings | fromjson) catch {"_parse_error": "invalid Settings JSON"}) | to_entries | map("      \(.key): \(.value | tojson)") | .[]),
        ""'
