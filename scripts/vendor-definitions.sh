#!/usr/bin/env bash
# Refresh the embedded Jackett definition snapshot. Pin JACKETT_REF to a commit
# SHA for reproducible builds. Run via `make vendor-defs` or CI. The vendored
# files are NEVER edited by hand (AGENTS.md) — this script is the only writer.
set -euo pipefail

JACKETT_REPO="${JACKETT_REPO:-https://github.com/Jackett/Jackett}"
JACKETT_REF="${JACKETT_REF:-b4140c7f8c1f4e1818d5792a6518e7f91861466d}"   # pinned 2026-06-11
DEST="internal/indexer/definitions/vendor"
SRC="src/Jackett.Common/Definitions"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Vendoring Jackett definitions from ${JACKETT_REPO} @ ${JACKETT_REF}"
git -C "$tmp" init -q
git -C "$tmp" remote add origin "$JACKETT_REPO"
git -C "$tmp" sparse-checkout init --cone
git -C "$tmp" sparse-checkout set "$SRC"
git -C "$tmp" fetch -q --depth 1 origin "$JACKETT_REF"
git -C "$tmp" checkout -q FETCH_HEAD

# Replace the snapshot wholesale; keep the README that documents the dir.
find "$DEST" -maxdepth 1 -type f ! -name 'README.md' -delete 2>/dev/null || true
cp -a "$tmp/$SRC/." "$DEST/"
git -C "$tmp" rev-parse HEAD > "$DEST/.jackett-ref"

count="$(find "$DEST" -name '*.yml' | wc -l | tr -d ' ')"
echo "Vendored ${count} definitions to ${DEST} (ref $(cat "$DEST/.jackett-ref"))"
