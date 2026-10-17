# Screenshots and GIFs

All three captures exist. This file records what each one shows and how to
retake it, for example after a UI change.

| File | What it shows | Used in the README |
|---|---|---|
| `demo-booking.gif` | The web app on the live demo: pick a mode, search, choose a class, confirm and pay, "Booking confirmed!" | "What it looks like" |
| `jaeger-booking-trace.gif` | Jaeger on the live demo: choose the service, find traces, open one train booking and its span waterfall | "Observability" |
| `ci-green.png` | The Actions run with every job green | "Engineering highlights" |

## Before you publish a capture

Look at a few frames first. In the current captures the window title bar, the
tab strip and the browser profile icon are visible, and the app header shows the
name typed into the sign-in box. If you do not want any of that public, sign in
with a demo name such as "Demo User" and crop or hide the browser chrome.

GIFs over about 5 MB load slowly on GitHub. Shrink them with:

```bash
pip install pillow
python3 scripts/shrink-gif.py raw.gif docs/assets/demo-booking.gif
```

The defaults (960 px wide, every second frame, 128 colors) took the raw
recordings from 7 MB and 15 MB to 1.6 MB and 3.4 MB and kept the text readable.

## Retaking from the live demo

- Booking GIF: open https://34-14-221-81.sslip.io, record the browser tab at
  about 1280x720 with ScreenToGif or similar, pick a mode, search, click a class,
  confirm, and stop on "Booking confirmed!".
- Jaeger GIF: after at least one booking, open
  https://jaeger.34-14-221-81.sslip.io, set **Service** to `gateway`, click
  **Find Traces**, open the newest booking trace, and let the recording finish on
  the span waterfall.

The trace in the current GIF is one train booking, `POST /v1/bookings/train`,
about 101 ms, 5 services and 11 spans, with the saga calling inventory (hold),
payment, supplier-sim and inventory again (confirm), in that order.

## Retaking from a GitHub Codespace

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
   | 16686 | Jaeger traces | the trace GIF |
   | 9090 | Prometheus | not needed |

   Click the globe icon next to a port to open it in your browser. The URL looks
   like `https://<codespace-name>-8080.app.github.dev`. Ports are private by
   default, which is fine as long as that browser is signed in to GitHub. Do not
   make a port public unless you have to, and set it back to private after.
4. Record the booking and the Jaeger search-to-waterfall flow exactly as in the
   live-demo steps above, using the forwarded URLs.
5. Stop the codespace when you are done so it does not use your free hours.

## CI screenshot

Open the latest run at
https://github.com/Muditjain13/RoadRunnerProject/actions and screenshot the job
list and the job graph.

## Markdown used in the README

```markdown
![Booking a train ticket on the live demo](docs/assets/demo-booking.gif)
![One train booking traced across 5 services in Jaeger](docs/assets/jaeger-booking-trace.gif)
![CI: every job green](docs/assets/ci-green.png)
```

## Real command output, no capture needed

- `smoke-run.txt`: the compose smoke test from a green CI run.
- `concurrency-tests.txt`: the concurrency and hard-case unit tests.

## Not captured yet

The "payment succeeded, supplier declined, automatic refund" scenario from the
README's hard case needs the supplier simulator forced to decline, and the
compose stack has no switch for that. If you want a GIF of it, ask for a
`chaos` compose override first.
