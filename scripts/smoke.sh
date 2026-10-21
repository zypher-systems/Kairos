#!/usr/bin/env bash
# Compose smoke: healthz, first-run setup, create a local job, run it, wait for success.
set -euo pipefail

BASE="${KAIROS_BASE_URL:-http://127.0.0.1:${KAIROS_HTTP_PORT:-8080}}"
COOKIE_JAR="$(mktemp)"
trap 'rm -f "$COOKIE_JAR"' EXIT

wait_healthy() {
  local i
  for i in $(seq 1 60); do
    if curl -sf "$BASE/healthz" | grep -q healthy; then
      return 0
    fi
    sleep 2
  done
  echo "healthz did not become healthy at $BASE" >&2
  return 1
}

echo "== healthz"
wait_healthy
curl -sf "$BASE/healthz"
echo

echo "== setup"
curl -sf -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H "Content-Type: application/json" \
  -X POST "$BASE/api/v1/auth/setup" \
  -d '{"username":"smoke","password":"smoke-pass-123"}' \
  || curl -sf -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
    -H "Content-Type: application/json" \
    -X POST "$BASE/api/v1/auth/login" \
    -d '{"username":"smoke","password":"smoke-pass-123"}'
echo

echo "== create job"
JOB_JSON="$(curl -sf -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H "Content-Type: application/json" \
  -X POST "$BASE/api/v1/jobs" \
  -d '{
    "name":"smoke-echo",
    "schedule_type":"interval",
    "interval_seconds":3600,
    "timezone":"UTC",
    "target_type":"local",
    "exec_mode":"shell",
    "shell_script":"echo kairos-smoke-ok",
    "enabled":false
  }')"
JOB_ID="$(echo "$JOB_JSON" | jq -r .id)"
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
  echo "create job failed: $JOB_JSON" >&2
  exit 1
fi
echo "job $JOB_ID"

echo "== run now"
RUN_JSON="$(curl -sf -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H "Content-Type: application/json" \
  -X POST "$BASE/api/v1/jobs/${JOB_ID}/run")"
RUN_ID="$(echo "$RUN_JSON" | jq -r .id)"
echo "run $RUN_ID"

echo "== wait for success"
for i in $(seq 1 30); do
  STATUS="$(curl -sf -c "$COOKIE_JAR" -b "$COOKIE_JAR" "$BASE/api/v1/runs/${RUN_ID}" | jq -r .status)"
  echo "  status=$STATUS"
  if [[ "$STATUS" == "succeeded" ]]; then
    echo "smoke ok"
    exit 0
  fi
  if [[ "$STATUS" == "failed" || "$STATUS" == "canceled" ]]; then
    echo "run ended $STATUS" >&2
    curl -sf -c "$COOKIE_JAR" -b "$COOKIE_JAR" "$BASE/api/v1/runs/${RUN_ID}/logs" || true
    exit 1
  fi
  sleep 2
done

echo "run did not finish in time" >&2
exit 1
