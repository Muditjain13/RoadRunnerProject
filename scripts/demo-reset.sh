#!/usr/bin/env bash
# Nightly reset for the public demo. Wipes the app data volumes and brings the
# stack back up from a freshly seeded state.
#
# Two reasons to do this on a schedule, not just to tidy up:
#   1. Visitors book seats, and a demo that is half sold out after a week
#      stops being a good demo.
#   2. The seed generates trips for the next 7 days starting from the day it
#      runs. Without a reset the search results run out a week after deploy.
#
# What it must NOT wipe: Caddy's certificate volumes. `docker compose down -v`
# removes every volume of the project, including caddy-data and caddy-config,
# so Caddy would request a brand new Let's Encrypt certificate every night.
# Let's Encrypt allows only 5 certificates per week for the same set of names,
# so HTTPS would break within a week. The volumes to keep are listed in KEEP
# below; every other volume the compose files declare is treated as demo data
# and deleted, so a data volume added later is wiped by default.
#
# Example cron entry (03:30 server time, log to a file):
#   30 3 * * * /home/ubuntu/RoadRunnerProject/scripts/demo-reset.sh >> /var/log/demo-reset.log 2>&1
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f deploy/compose/docker-compose.yml -f deploy/oracle/docker-compose.demo.yml)
GATEWAY="${GATEWAY:-http://127.0.0.1:8080}"
PROJECT="${COMPOSE_PROJECT_NAME:-transport-app}"
KEEP=" caddy-data caddy-config "

echo "$(date -u +%FT%TZ) demo reset: stopping containers (volumes are removed selectively below)"
"${COMPOSE[@]}" down --remove-orphans

# Delete the app data volumes, keep everything listed in KEEP.
while read -r vol; do
  [[ -n "$vol" ]] || continue
  if [[ "$KEEP" == *" $vol "* ]]; then
    echo "$(date -u +%FT%TZ) demo reset: keeping volume $vol"
    continue
  fi
  # A volume that does not exist yet (first run) is fine; one that exists and
  # cannot be removed is an error, since stale data would silently survive.
  if docker volume inspect "${PROJECT}_${vol}" >/dev/null 2>&1; then
    echo "$(date -u +%FT%TZ) demo reset: removing volume $vol"
    docker volume rm "${PROJECT}_${vol}" >/dev/null
  fi
done < <("${COMPOSE[@]}" config --volumes)

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
