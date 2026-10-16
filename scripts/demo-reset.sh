#!/usr/bin/env bash
# Nightly reset for the public demo. Wipes every volume and brings the stack
# back up from a freshly seeded state.
#
# Two reasons to do this on a schedule, not just to tidy up:
#   1. Visitors book seats, and a demo that is half sold out after a week
#      stops being a good demo.
#   2. The seed generates trips for the next 7 days starting from the day it
#      runs. Without a reset the search results run out a week after deploy.
#
# Example cron entry (03:30 server time, log to a file):
#   30 3 * * * /home/ubuntu/RoadRunnerProject/scripts/demo-reset.sh >> /var/log/demo-reset.log 2>&1
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f deploy/compose/docker-compose.yml -f deploy/oracle/docker-compose.demo.yml)
GATEWAY="${GATEWAY:-http://127.0.0.1:8080}"

echo "$(date -u +%FT%TZ) demo reset: stopping and wiping volumes"
"${COMPOSE[@]}" down -v --remove-orphans

echo "$(date -u +%FT%TZ) demo reset: starting"
"${COMPOSE[@]}" up -d

# Wait for the one-shot seed job to finish and the gateway to answer, so a
# failed reset is loud in the log instead of silently leaving an empty demo.
for _ in $(seq 1 120); do
  id="$("${COMPOSE[@]}" ps -aq seed | head -n1)"
  if [[ -n "$id" && "$(docker inspect -f '{{.State.Status}}' "$id")" == "exited" ]]; then
    code="$(docker inspect -f '{{.State.ExitCode}}' "$id")"
    [[ "$code" == "0" ]] || { echo "demo reset FAILED: seed exited with code $code" >&2; exit 1; }
    break
  fi
  sleep 5
done

for _ in $(seq 1 60); do
  if curl -fs "$GATEWAY/healthz" >/dev/null; then
    echo "$(date -u +%FT%TZ) demo reset: done, gateway healthy"
    docker image prune -f >/dev/null
    exit 0
  fi
  sleep 5
done

echo "demo reset FAILED: gateway did not become healthy" >&2
exit 1
