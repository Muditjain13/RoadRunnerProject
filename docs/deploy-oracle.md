# Deploying the live demo on Oracle Cloud Always Free

Status: **a plan, nothing is deployed yet.** The repo changes this plan needs
are done and checked in CI. What CI cannot check is marked "unverified" below.

The goal is one public URL where a visitor can search, pick a seat and book,
running the existing Docker Compose stack unchanged on a free ARM VM.

## The VM

| | |
|---|---|
| Shape | `VM.Standard.A1.Flex`, 4 OCPU and 24 GB RAM (the Always Free A1 allowance) |
| Image | Ubuntu 24.04 (aarch64) |
| Disk | 50 GB boot volume is plenty; the small seed profile is under 1 GB of data |
| Extras | Docker Engine and the Compose plugin, version 2.24.4 or newer |

## arm64 compatibility

The Go services are built on the VM by the existing `Dockerfile`. It sets no
`GOARCH`, so building on an arm64 host produces arm64 binaries. Every image
the stack uses was run on an arm64 GitHub runner in CI (`ubuntu-24.04-arm`),
and the full flow passed there: migrate, seed, search, booking, and the UI
test in headless Chromium.

| Image | Role | arm64 evidence |
|---|---|---|
| `golang:1.26-bookworm`, `gcr.io/distroless/static-debian12` | build and runtime base of all 8 Go services plus `seed` | built and run in the arm64 CI job |
| `mysql:8.4` | inventory, booking and payment data | ran in the arm64 CI job |
| `mongo:7` | catalog | ran in the arm64 CI job. Mongo 5 and later needs an ARMv8.2 CPU; Ampere A1 qualifies |
| `redis:7-alpine` | search cache | ran in the arm64 CI job |
| `redpandadata/redpanda:v24.2.7` | Kafka-compatible broker | ran in the arm64 CI job |
| `jaegertracing/all-in-one:1.60`, `prom/prometheus:v2.55.1` | traces and metrics | ran in the arm64 CI job |
| `migrate/migrate:v4.17.0` | one-shot migrations | ran in the arm64 CI job |
| `caddy:2` | HTTPS proxy, only in the demo profile | ran in the `public demo profile (arm64)` CI job |

CI runners are not Ampere hardware, so a run on the real VM is still the final
check (see Risks).

## What to expose

The base compose file publishes every port for local development. The
`deploy/oracle/docker-compose.demo.yml` override removes them, and CI enforces
the result: the `public demo profile (arm64)` job fails if anything other than
80 and 443 is published on all interfaces.

| Port | Who can reach it | Why |
|---|---|---|
| 443 and 80 | the internet | Caddy: HTTPS for the web app and API, port 80 for the certificate challenge and redirect |
| 22 | your own IP only | SSH, restricted in the VCN security list |
| 8080 | the VM itself (127.0.0.1) | gateway, only reachable through Caddy |
| 16686 | the VM itself, and the internet read-only via `jaeger.<domain>` | Jaeger UI. Caddy lets only GET and HEAD through, and the OTLP ingest ports are not published at all |
| 9090 | the VM itself | Prometheus, use an SSH tunnel |
| everything else | nobody | databases, broker, cache and the gRPC services stay on the compose network |

Docker publishes ports by editing iptables directly, which bypasses `ufw`. Do
not rely on a host firewall for this. The two real controls are the override
file and the VCN security list, which should allow ingress on 22 (your IP), 80
and 443 only. Oracle's Ubuntu images also ship restrictive default iptables
rules, so you may need to allow 80 and 443 there too (unverified, check on the
VM).

## Rate limiting at the gateway

The gateway had a per-IP token bucket (10 requests per second, burst of 20).
Two things would have broken it on a public server, both fixed:

| Problem | Fix |
|---|---|
| Behind Caddy every visitor arrives from the proxy's address, so everyone would share one bucket | `GATEWAY_TRUSTED_PROXIES`: when the direct peer is a trusted proxy, the client is the right-most `X-Forwarded-For` entry that is not itself trusted. A header sent by an untrusted peer is ignored, so it cannot be spoofed |
| Buckets were never removed, so the map grows with every distinct visitor | idle buckets are swept after 10 minutes |

It also gets a separate, stricter budget for writes. `POST /v1/bookings` runs
the whole saga and writes to MySQL, so the demo profile allows a burst of 5,
then one every 5 seconds, per visitor. Reads keep the 10 per second budget.
The UI asks for seat availability once per class per trip on every search, and
that stays well inside the read budget.

| Variable | Default | Demo profile |
|---|---|---|
| `GATEWAY_RATE_RPS`, `GATEWAY_RATE_BURST` | 10, 20 | 10, 20 |
| `GATEWAY_WRITE_RATE_RPS`, `GATEWAY_WRITE_RATE_BURST` | off | 0.2, 5 |
| `GATEWAY_TRUSTED_PROXIES` | none | private ranges only |

Unit tests cover the proxy handling, spoofing, the write limit and eviction.
The CI job above also checks that rapid POSTs through Caddy get a 429 while
reads are unaffected.

## Nightly reset

`scripts/demo-reset.sh` wipes the volumes and starts the stack from a fresh
seed. It matters for two reasons: visitors consume seats, and the seed
generates trips for the 7 days after the day it runs, so without a reset the
search results run dry a week after deploy.

```bash
# crontab -e on the VM (03:30 server time)
30 3 * * * /home/ubuntu/RoadRunnerProject/scripts/demo-reset.sh >> /var/log/demo-reset.log 2>&1
```

Expect a short outage while it runs. The script exits non-zero if the seed
fails or the gateway does not come back, so a failed reset is visible in the
log. I have not timed it on the real VM (unverified).

## Steps, when you decide to deploy

1. Create the A1 instance, add a public IP, and set the VCN ingress rules above.
2. Install Docker Engine and the Compose plugin; clone the repo.
3. Pick a host name. The free route is `<ip-with-dashes>.sslip.io`, for example
   `203-0-113-10.sslip.io`, which also gives you `jaeger.203-0-113-10.sslip.io`.
   A DuckDNS name or your own domain works the same.
4. Start it:
   ```bash
   export DEMO_DOMAIN=203-0-113-10.sslip.io
   docker compose -f deploy/compose/docker-compose.yml \
     -f deploy/oracle/docker-compose.demo.yml up -d --build
   ```
5. Add the cron line above, then run `scripts/check-demo-profile.sh` once.

## Risks and unknowns

- Oracle can reclaim Always Free instances it considers idle, and A1 capacity
  is often unavailable in popular regions ("out of host capacity"). Check
  Oracle's current terms before relying on it.
- Let's Encrypt applies rate limits per registered domain, and `sslip.io` is
  shared by many users. If certificate issuance fails, use DuckDNS or your own
  domain (unverified).
- The booking saga's supplier is a simulator, so there is no real payment or
  real supplier behind this demo.
- Traces in the public Jaeger can include the names visitors type into the
  sign-in box. Remove the `jaeger.` block from `deploy/oracle/Caddyfile` to keep
  Jaeger private.

## What changed in the repo for this

| Change | Where |
|---|---|
| Public-profile compose override and Caddy config | `deploy/oracle/` |
| Proxy-aware, evicting, write-limited rate limiter, configured from the environment | `internal/gateway/middleware/ratelimit.go`, `cmd/gateway/main.go` |
| Nightly reset | `scripts/demo-reset.sh` |
| Exposure, HTTPS and rate-limit check | `scripts/check-demo-profile.sh` |
| Smoke test on amd64 and arm64, UI test, and demo-profile job | `.github/workflows/ci.yml` |
