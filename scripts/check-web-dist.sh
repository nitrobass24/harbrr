#!/bin/sh
set -eu

if [ ! -f web/dist/index.html ]; then
  echo "ERROR: web/dist is empty (no index.html) - run make web-build before a release build"
  exit 1
fi
