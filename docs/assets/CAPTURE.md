# Screenshots and GIFs still to capture

Everything here needs a screen, so it cannot be generated from a script. Save
each file under `docs/assets/` with exactly the name shown, then add the
Markdown line to the README section named in the last column.

| # | File | What to capture | README spot |
|---|---|---|---|
| 1 | `demo-booking.gif` | The web app booking flow, about 15 seconds: pick PNQ to BOM, mode Bus, search, click a class, select one seat, confirm, enter a name, show "Booking confirmed!" | "What it looks like", above the two existing screenshots |
| 2 | `jaeger-booking-trace.png` | One booking as a trace waterfall across gateway, booking, inventory, payment and supplier-sim | "Observability" |
| 3 | `ci-green.png` | The Actions run with every job green | "Engineering highlights" |

## Taking #1 and #2 from a GitHub Codespace

This needs no local Docker. The dev container config has not been opened in a
real Codespace yet, so if a step below differs from what you see, trust the
screen and tell me what changed.

1. On the repo page click **Code, Codespaces, Create codespace on main**, or use
   the "Open in GitHub Codespaces" badge in the README. Pick a **4-core** machine
   or larger; the stack runs about 14 containers.
2. Wait for the setup to finish (Go, Node, Docker-in-Docker, `jq`). Open the
   terminal and start the stack:
   ```bash
   docker compose -f deploy/compose/docker-compose.yml up -d --build
   docker compose -f deploy/compose/docker-compose.yml ps -a seed   # wait for Exited (0)
   ```
   The first build takes several minutes.
3. Open the **Ports** tab. Three ports are forwarded:

   | Port | Label | Use |
   |---|---|---|
   | 8080 | Web app and REST API (gateway) | the booking GIF |
   | 16686 | Jaeger traces | the trace screenshot |
   | 9090 | Prometheus | not needed |

   Click the globe icon next to a port to open it in your browser. The URL looks
   like `https://<codespace-name>-8080.app.github.dev`. Ports are private by
   default, which is fine as long as that browser is signed in to GitHub. Do not
   make a port public unless you have to, and set it back to private after.
4. **Booking GIF (#1).** Record the browser tab, not VS Code, with ScreenToGif
   or similar at about 1280x720. In the app choose From Pune, To Mumbai, Bus,
   click **Search trips**, click a class chip that says "left", pick one seat,
   click the confirm button, type any name, and stop recording on "Booking
   confirmed!". Keep the file under 3 MB.
5. **Jaeger screenshot (#2).** After at least one booking, open the 16686 URL.
   Set **Service** to `gateway`, click **Find Traces**, and sort by **Most
   Spans**. The trace with the most spans is the booking, because the saga calls
   inventory, payment and the supplier simulator. Open it, expand all spans, and
   screenshot the waterfall. I have not seen the exact span names in this
   environment, so go by span count, not by name.
6. Stop the codespace when you are done so it does not use your free hours.

## #3 CI screenshot

Open the latest run at
https://github.com/Muditjain13/RoadRunnerProject/actions and screenshot the job
graph.

## Markdown to paste once the files exist

```markdown
![Booking flow in the web app](docs/assets/demo-booking.gif)
![One booking traced across services in Jaeger](docs/assets/jaeger-booking-trace.png)
![CI: every job green](docs/assets/ci-green.png)
```

## Already generated (real output, no capture needed)

- `smoke-run.txt`: the compose smoke test from a green CI run.
- `concurrency-tests.txt`: the concurrency and hard-case unit tests.

## Not captured yet

The "payment succeeded, supplier declined, automatic refund" scenario from the
README's hard case needs the supplier simulator forced to decline, and the
compose stack has no switch for that. If you want a GIF of it, ask for a
`chaos` compose override first.
