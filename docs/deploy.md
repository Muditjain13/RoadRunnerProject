# Deploying the public demo

Status: **live** at https://34-14-221-81.sslip.io on Google Cloud, with a
read-only Jaeger at https://jaeger.34-14-221-81.sslip.io. It runs the repo's
Docker Compose stack plus the public-demo profile in `deploy/oracle/`. The
directory is named `oracle` because Oracle Cloud was the first target; nothing
in it is specific to Oracle.

## What was checked on the live server

Probed from outside on 17 Oct 2026:

| Check | Result |
|---|---|
| HTTPS | valid Let's Encrypt certificate for `34-14-221-81.sslip.io`, issued 17 Oct 2026, expires 15 Jan 2027 |
| Reverse proxy | responses carry `Via: 1.1 Caddy` |
| Jaeger is read-only | `GET` on `jaeger.` returns 200, `POST` returns 405 |
| Ports that answer | 22, 80 and 443 |
| Ports that do not answer | 3306, 27017, 6379, 9092, 8080, 9090, 16686, 4317 and 8082 |

That matches the demo profile: only Caddy is public, and the databases, broker,
cache, gateway, Prometheus and the Jaeger ingest port are not reachable.

Not recorded in this repo, so not stated here: the machine type, region and CPU
architecture, how the firewall rules are defined, and whether the nightly reset
cron entry is installed. Two things worth confirming:

- **SSH on port 22 answers.** The probe ran from the project owner's own
  machine, so it cannot show whether 22 is restricted. If the VM is on GCP's
  `default` network, the stock `default-allow-ssh` rule opens 22 to the whole
  internet (from GCP's defaults, not checked on this project). Restrict the source
  range, or use IAP or OS Login instead.
- **Caddy advertises HTTP/3** (`Alt-Svc: h3=":443"`) but the override publishes
  only TCP 443, so browsers fall back to HTTP/2. This is harmless. Add
  `"443:443/udp"` to the `caddy` ports if you want HTTP/3.

## How it is put together

| Piece | Where |
|---|---|
| Base stack: 8 Go services, MySQL, Mongo, Redis, Redpanda, Jaeger, Prometheus, the one-shot `migrate` and `seed` jobs | `deploy/compose/docker-compose.yml` |
| Public profile: no published ports except what Caddy needs, loopback-only admin ports, memory caps, rate limits | `deploy/oracle/docker-compose.demo.yml` |
| HTTPS and the read-only Jaeger host | `deploy/oracle/Caddyfile` |

Start it with the host name you control:

```bash
export DEMO_DOMAIN=34-14-221-81.sslip.io
docker compose -f deploy/compose/docker-compose.yml \
  -f deploy/oracle/docker-compose.demo.yml up -d --build
```

The host name is `<ip-with-dashes>.sslip.io`, which also resolves
`jaeger.<ip-with-dashes>.sslip.io`. A DuckDNS name or your own domain works the
same. You need Docker Engine and the Compose plugin, version 2.24.4 or newer.

## What to expose

The base compose file publishes every port for local development. The demo
override removes them, and CI enforces the result: the `public demo profile
(arm64)` job fails if anything other than 80 and 443 is published on all
interfaces.

| Port | Who can reach it | Why |
|---|---|---|
| 443 and 80 | the internet | Caddy: HTTPS for the web app and API, port 80 for the certificate challenge and redirect |
| 22 | your own IP only | SSH, restricted in the cloud firewall |
| 8080 | the VM itself (127.0.0.1) | gateway, only reachable through Caddy |
| 16686 | the VM itself, and the internet read-only via `jaeger.<domain>` | Jaeger UI. Caddy lets only GET and HEAD through, and the OTLP ingest ports are not published at all |
| 9090 | the VM itself | Prometheus, use an SSH tunnel |
| everything else | nobody | databases, broker, cache and the gRPC services stay on the compose network |

Docker publishes ports by editing iptables directly, which bypasses `ufw`. Do
not rely on a host firewall for this. The two real controls are the override
file and the cloud firewall (a GCP VPC firewall rule, an Oracle VCN security
list), which should allow ingress on 22 (your IP only), 80 and 443.

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

`scripts/demo-reset.sh` wipes the app data volumes (`mysql-data`, `mongo-data`
and `redpanda-data`) and starts the stack from a fresh seed. It matters for two
reasons: visitors consume seats, and the seed generates trips for the 7 days
after the day it runs, so without a reset the search results run dry a week
after deploy.

It deliberately keeps `caddy-data` and `caddy-config`. `docker compose down -v`
removes every volume of the project, Caddy's included, so Caddy would request a
brand new Let's Encrypt certificate after every reset. Let's Encrypt allows only
5 certificates per week for the same set of names, so HTTPS would break within a
week. The script runs `down` without `-v`, then deletes every volume the compose
files declare except the ones in its `KEEP` list. A data volume added later is
therefore wiped by default, and only the certificate volumes survive.

`scripts/check-demo-reset.sh` runs in the `public demo profile` CI job. It records
each volume's creation time and a hash of Caddy's certificate authority, runs the
real reset, then fails unless both Caddy volumes and the authority are unchanged,
the three data volumes were recreated, and HTTPS still answers.

```bash
# crontab -e on the VM (03:30 server time)
30 3 * * * /path/to/RoadRunnerProject/scripts/demo-reset.sh >> /var/log/demo-reset.log 2>&1
```

Expect a short outage while it runs. The script exits non-zero if the seed
fails or the gateway does not come back, so a failed reset is visible in the
log. The outage length has not been timed on the real VM.

## CPU architecture

The Go services are built on the host by the existing `Dockerfile`. It sets no
`GOARCH`, so an arm64 host produces arm64 binaries and an amd64 host produces
amd64. CI runs the full flow on both: migrate, seed, search, booking, and the UI
test in headless Chromium, on `ubuntu-latest` (amd64) and `ubuntu-24.04-arm`
(arm64). The public-demo profile, including Caddy, runs on arm64 in CI.

| Image | Role |
|---|---|
| `golang:1.26-bookworm`, `gcr.io/distroless/static-debian12` | build and runtime base of all 8 Go services plus `seed` |
| `mysql:8.4` | inventory, booking and payment data |
| `mongo:7` | catalog (Mongo 5 and later needs an ARMv8.2 CPU on arm64) |
| `redis:7-alpine` | search cache |
| `redpandadata/redpanda:v24.2.7` | Kafka-compatible broker |
| `jaegertracing/all-in-one:1.60`, `prom/prometheus:v2.55.1` | traces and metrics |
| `migrate/migrate:v4.17.0` | one-shot migrations |
| `caddy:2` | HTTPS proxy, only in the demo profile |

## Alternative target: Oracle Cloud Always Free

This was the original plan and is untested on real Oracle hardware. Everything
above applies unchanged. Oracle specifics:

- Shape `VM.Standard.A1.Flex`, up to 4 OCPU and 24 GB RAM free, Ubuntu 24.04
  aarch64. Mongo needs ARMv8.2, which Ampere A1 provides.
- Oracle can reclaim Always Free instances it considers idle, and A1 capacity is
  often unavailable in popular regions ("out of host capacity"). Check Oracle's
  current terms before relying on it.
- Oracle's Ubuntu images ship restrictive default iptables rules, so you may need
  to allow 80 and 443 there as well as in the VCN security list.

## Risks and notes

- The booking saga's supplier is a simulator, so there is no real payment or
  real supplier behind this demo.
- Traces in the public Jaeger can include the names visitors type into the
  sign-in box. Remove the `jaeger.` block from `deploy/oracle/Caddyfile` to keep
  Jaeger private.
- `sslip.io` is a shared service. Certificate issuance worked for this host name,
  but if it ever fails, use DuckDNS or your own domain.

## What changed in the repo for this

| Change | Where |
|---|---|
| Public-profile compose override and Caddy config | `deploy/oracle/` |
| Proxy-aware, evicting, write-limited rate limiter, configured from the environment | `internal/gateway/middleware/ratelimit.go`, `cmd/gateway/main.go` |
| Nightly reset | `scripts/demo-reset.sh` |
| Exposure, HTTPS and rate-limit check | `scripts/check-demo-profile.sh` |
| Reset keeps the certificate volumes, checked in CI | `scripts/check-demo-reset.sh` |
| Smoke test on amd64 and arm64, UI test, and demo-profile job | `.github/workflows/ci.yml` |
