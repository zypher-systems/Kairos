#!/usr/bin/env bash
# Fail if go cover profile total is below COVER_MIN (percent).
set -euo pipefail
MIN="${COVER_MIN:-30}"
FILE="${1:?cover profile required}"
pct=$(go tool cover -func="$FILE" | awk '/^total:/ {gsub(/%/,"",$3); print $3}')
awk -v pct="$pct" -v min="$MIN" 'BEGIN {
  printf "coverage %s%% (min %s%%)\n", pct, min
  exit (pct+0 >= min+0 ? 0 : 1)
}'
