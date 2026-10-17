#!/usr/bin/env bash
# Checks the public-demo profile (deploy/oracle) after it has been started:
#   1. the only ports published on all interfaces are 80 and 443,
#   2. HTTPS through Caddy reaches the gateway,
#   3. the write rate limit works behind the proxy.
# Run it with DEMO_DOMAIN set to the same value the stack was started with.
set -euo pipefail

DEMO_DOMAIN="${DEMO_DOMAIN:-localhost}"
PROJECT="${COMPOSE_PROJECT_NAME:-transport-app}"
fail() { echo "DEMO PROFILE FAIL: $*" >&2; exit 1; }

# 1. Exposure: anything bound to 0.0.0.0 or [::] is reachable from outside.
exposed="$(docker ps --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Names}}|{{.Ports}}' |
  awk -F'|' '{ n = split($2, p, ", "); for (i = 1; i <= n; i++) if (p[i] ~ /^(0\.0\.0\.0|\[::\]):[0-9]+->/) { sub(/->.*/, "", p[i]); sub(/^.*:/, "", p[i]); print p[i] "|" $1 } }' |
  sort -u)"
echo "ports published on all interfaces:"
echo "${exposed:-  (none)}"
unexpected="$(echo "$exposed" | awk -F'|' 'NF && $1 != "80" && $1 != "443" { print }')"
[[ -z "$unexpected" ]] || fail "unexpected publicly published ports:
$unexpected"
echo "$exposed" | grep -q "^80|" || fail "port 80 is not published"
echo "$exposed" | grep -q "^443|" || fail "port 443 is not published"

# 2. HTTPS through the proxy. -k because with localhost Caddy uses its own CA.
for _ in $(seq 1 30); do
  code="$(curl -sk -o /dev/null -w '%{http_code}' "https://$DEMO_DOMAIN/healthz" || true)"
  [[ "$code" == "200" ]] && break
  sleep 2
done
[[ "$code" == "200" ]] || fail "https://$DEMO_DOMAIN/healthz returned $code, want 200"
echo "HTTPS through Caddy OK"

# 3. Write limit. Unauthenticated POSTs are rejected with 401 by the handler,
# but the limiter runs first, so a burst of them must start returning 429.
first=""; got429=0
for i in $(seq 1 15); do
  code="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "https://$DEMO_DOMAIN/v1/bookings")"
  [[ -n "$first" ]] || first="$code"
  [[ "$code" == "429" ]] && got429=1
done
[[ "$first" != "429" ]] || fail "first POST was already rate limited"
[[ "$got429" == "1" ]] || fail "15 rapid POSTs never hit the write rate limit"
code="$(curl -sk -o /dev/null -w '%{http_code}' "https://$DEMO_DOMAIN/healthz")"
[[ "$code" == "200" ]] || fail "reads must not be write-limited, /healthz returned $code"
echo "write rate limit OK (POSTs limited, GETs unaffected)"

echo "DEMO PROFILE PASS"
