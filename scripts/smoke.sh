#!/usr/bin/env bash
# Smoke test for the docker compose stack. Assumes `docker compose up -d`
# has already been run. Waits for the one-shot migrate and seed jobs and the
# gateway, then makes one real search and one real booking through the
# gateway. Exits non-zero on the first failure.
set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose/docker-compose.yml}"
GATEWAY="${GATEWAY:-http://localhost:8080}"
compose() { docker compose -f "$COMPOSE_FILE" "$@"; }

fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }

wait_for_job() {
  local svc="$1" timeout="$2" waited=0 id state code
  echo "waiting for one-shot job: $svc"
  while ((waited < timeout)); do
    id="$(compose ps -aq "$svc" | head -n1)"
    if [[ -n "$id" ]]; then
      state="$(docker inspect -f '{{.State.Status}}' "$id")"
      code="$(docker inspect -f '{{.State.ExitCode}}' "$id")"
      if [[ "$state" == "exited" ]]; then
        [[ "$code" == "0" ]] || fail "$svc exited with code $code"
        echo "$svc finished"
        return 0
      fi
    fi
    sleep 5
    waited=$((waited + 5))
  done
  fail "$svc did not finish within ${timeout}s"
}

wait_for_job migrate 300
wait_for_job seed 900

echo "waiting for gateway at $GATEWAY"
for _ in $(seq 1 60); do
  if curl -fs "$GATEWAY/healthz" >/dev/null; then break; fi
  sleep 5
done
curl -fs "$GATEWAY/healthz" >/dev/null || fail "gateway never became healthy"

# 0. The UI is served by the gateway on the same origin and uses only
# relative URLs, so it works unchanged behind any public host name.
for p in / /static/app.js /static/style.css; do
  code="$(curl -s -o /dev/null -w '%{http_code}' "$GATEWAY$p")"
  [[ "$code" == "200" ]] || fail "UI asset $p returned HTTP $code"
done
curl -s "$GATEWAY/" | grep -q "RoadRunner" || fail "index page does not look like the RoadRunner UI"
if curl -s "$GATEWAY/static/app.js" | grep -nE 'https?://'; then
  fail "UI hardcodes an absolute URL, it must use relative paths"
fi
echo "UI served OK, relative URLs only"

date_str="$(date -u -d tomorrow +%F)"

# 1. Search: find a bus trip on any seeded route.
trip_json=""
for pair in "PNQ BOM" "BOM PNQ" "DEL JAI" "JAI DEL" "BLR MAA" "MAA BLR"; do
  read -r origin dest <<<"$pair"
  url="$GATEWAY/v1/search?origin=$origin&dest=$dest&date=$date_str&mode=bus"
  status="$(curl -s -o search.json -w '%{http_code}' "$url")"
  [[ "$status" == "200" ]] || fail "search $origin->$dest returned HTTP $status: $(cat search.json)"
  trip_json="$(jq -c '.trips[0] // empty' search.json)"
  [[ -n "$trip_json" ]] && break
done
[[ -n "$trip_json" ]] || fail "search returned no bus trips for any seeded route on $date_str"
trip_id="$(jq -r '.id' <<<"$trip_json")"
class="$(jq -r '.classes[0].code' <<<"$trip_json")"
fare="$(jq -r '.classes[0].fare_paise' <<<"$trip_json")"
echo "search OK: trip=$trip_id class=$class fare_paise=$fare"

# 2. Pick a free seat.
status="$(curl -s -o seats.json -w '%{http_code}' "$GATEWAY/v1/trips/$trip_id/seats?class=$class")"
[[ "$status" == "200" ]] || fail "list seats returned HTTP $status: $(cat seats.json)"
seat="$(jq -r '.seat_nos[0] // empty' seats.json)"
[[ -n "$seat" ]] || fail "no available seats on $trip_id"
echo "seat: $seat"

# 3. Book it: runs the whole saga (hold, pay, supplier confirm).
body="$(jq -nc --arg t "$trip_id" --arg s "$seat" --argjson a "$fare" \
  '{trip_id:$t, seat_nos:[$s], amount_paise:$a}')"
status="$(curl -s -o booking.json -w '%{http_code}' -X POST "$GATEWAY/v1/bookings" \
  -H 'Authorization: Bearer smoke-user' -H 'Content-Type: application/json' -d "$body")"
[[ "$status" == "201" ]] || fail "create booking returned HTTP $status: $(cat booking.json)"
booking_status="$(jq -r '.status' booking.json)"
[[ "$booking_status" == "STATUS_CONFIRMED" ]] || fail "booking status = $booking_status: $(cat booking.json)"
echo "booking OK: $(jq -c '{id,status,seat_nos,amount_paise}' booking.json)"

echo "SMOKE PASS"
