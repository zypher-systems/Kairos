#!/usr/bin/env bash
# SPA link check: every AppShell route must return 200 HTML from the server.
set -euo pipefail

BASE="${KAIROS_BASE_URL:-http://127.0.0.1:${KAIROS_HTTP_PORT:-8080}}"

wait_up() {
  local i
  for i in $(seq 1 30); do
    if curl -sf "$BASE/healthz" >/dev/null; then
      return 0
    fi
    sleep 2
  done
  echo "server not up at $BASE" >&2
  return 1
}

wait_up

fail=0
for path in / /jobs /jobs/new /runs /targets /settings /login; do
  code="$(curl -s -o /tmp/kairos-link-body -w "%{http_code}" "$BASE$path")"
  if [[ "$code" != "200" ]]; then
    echo "FAIL $path → $code" >&2
    fail=1
    continue
  fi
  if ! grep -qi "<!doctype html\|<html" /tmp/kairos-link-body; then
    echo "FAIL $path → 200 but not HTML" >&2
    fail=1
    continue
  fi
  echo "ok   $path"
done

rm -f /tmp/kairos-link-body
exit "$fail"
