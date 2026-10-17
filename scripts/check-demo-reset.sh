#!/usr/bin/env bash
# Runs scripts/demo-reset.sh against a running demo profile and checks the two
# things that matter:
#   1. Caddy's volumes survive, so its certificates are not re-issued nightly
#      (Let's Encrypt allows only 5 per week for the same names).
#   2. The app data volumes really are recreated, so the demo is reset.
#
# "Survive" is checked three ways: the volume's creation time is unchanged, the
# volume still exists, and Caddy's certificate authority file has the same
# SHA-256 after the reset. Run it with DEMO_DOMAIN set to the value the stack
# was started with.
set -euo pipefail

cd "$(dirname "$0")/.."

DEMO_DOMAIN="${DEMO_DOMAIN:-localhost}"
PROJECT="${COMPOSE_PROJECT_NAME:-transport-app}"
COMPOSE=(docker compose -f deploy/compose/docker-compose.yml -f deploy/oracle/docker-compose.demo.yml)
CA_FILE=/data/caddy/pki/authorities/local/root.crt
KEPT=(caddy-data caddy-config)
WIPED=(mysql-data mongo-data redpanda-data)

fail() { echo "DEMO RESET CHECK FAIL: $*" >&2; exit 1; }
created() { docker volume inspect -f '{{.CreatedAt}}' "${PROJECT}_$1" 2>/dev/null || echo "missing"; }
ca_hash() { "${COMPOSE[@]}" exec -T caddy sha256sum "$CA_FILE" | awk '{print $1}'; }

# Make sure Caddy has issued something (the first TLS request creates the CA).
for _ in $(seq 1 30); do
  curl -sk -o /dev/null "https://$DEMO_DOMAIN/healthz" && break
  sleep 2
done
declare -A before
for v in "${KEPT[@]}" "${WIPED[@]}"; do
  before[$v]="$(created "$v")"
  [[ "${before[$v]}" != "missing" ]] || fail "volume $v does not exist before the reset"
done
ca_before="$(ca_hash)"
[[ -n "$ca_before" ]] || fail "could not read Caddy's CA file before the reset"
echo "before: ca=$ca_before"

# CreatedAt has one second resolution, so make sure recreated volumes differ.
sleep 2
bash scripts/demo-reset.sh

for v in "${KEPT[@]}"; do
  after="$(created "$v")"
  [[ "$after" == "${before[$v]}" ]] || fail "volume $v was recreated (was ${before[$v]}, now $after), certificates would be re-issued"
  echo "kept:     $v (created ${after})"
done
for v in "${WIPED[@]}"; do
  after="$(created "$v")"
  [[ "$after" != "missing" ]] || fail "volume $v is missing after the reset"
  [[ "$after" != "${before[$v]}" ]] || fail "volume $v was not wiped by the reset"
  echo "wiped:    $v (created ${after})"
done

ca_after="$(ca_hash)"
[[ "$ca_after" == "$ca_before" ]] || fail "Caddy's CA changed across the reset ($ca_before to $ca_after)"
echo "after:  ca=$ca_after"

code="$(curl -sk -o /dev/null -w '%{http_code}' "https://$DEMO_DOMAIN/healthz")"
[[ "$code" == "200" ]] || fail "https://$DEMO_DOMAIN/healthz returned $code after the reset"
echo "HTTPS still works after the reset"

echo "DEMO RESET CHECK PASS"
