# Screenshots and GIFs still to capture

Everything here needs a screen, so it cannot be generated from a script. Save
each file under `docs/assets/` with exactly the name shown, then add the
Markdown line to the README section named in the last column.

Start the stack first:

```bash
docker compose -f deploy/compose/docker-compose.yml up -d --build
docker compose -f deploy/compose/docker-compose.yml ps -a seed   # wait for Exited (0)
```

| # | File | What to capture | How | README spot |
|---|---|---|---|---|
| 1 | `demo-booking.gif` | The web app booking flow, about 15 seconds: pick PNQ to BOM, mode bus, search, click a trip, select one seat, confirm, show the confirmed booking | Open http://localhost:8080 at about 1280x720. Record with ScreenToGif (Windows) at 10 to 12 fps. Keep it under 3 MB | "What it looks like", above the two existing screenshots |
| 2 | `jaeger-booking-trace.png` | One booking as a trace waterfall: the `POST /v1/bookings` root with its child spans across gateway, booking, inventory, payment and supplier-sim | Make one booking (see "Try it" in the README), open http://localhost:16686, Service = `gateway`, Find Traces, open the newest `POST /v1/bookings` trace, screenshot the full waterfall | "Observability" |
| 3 | `ci-green.png` | The Actions run with all three jobs green: build, vet, race tests; integration tests; docker compose smoke test | Open the latest run on https://github.com/Muditjain13/RoadRunnerProject/actions, screenshot the job summary graph | "Engineering highlights" |

## Markdown to paste once the files exist

```markdown
![Booking flow in the web app](docs/assets/demo-booking.gif)
![One booking traced across services in Jaeger](docs/assets/jaeger-booking-trace.png)
![CI: race tests, integration tests and compose smoke test all green](docs/assets/ci-green.png)
```

## Already generated (real output, no capture needed)

- `smoke-run.txt`: the compose smoke test from a green CI run.
- `concurrency-tests.txt`: the concurrency and hard-case unit tests.

## Not captured yet

The "payment succeeded, supplier declined, automatic refund" scenario from the
README's hard case needs the supplier simulator forced to decline, and the
compose stack has no switch for that. If you want a GIF of it, ask for a
`chaos` compose override first.
