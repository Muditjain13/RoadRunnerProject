// Minimal k6 scenario: search a bus route, list seats, book one. Run it with
// the compose stack up (see the README Quick start):
//
//   make load
//   k6 run -e VUS=5 -e DURATION=1m test/load/booking.js
//
// The gateway rate-limits each client IP (10 requests per second, burst 20, and
// all virtual users share one IP), and each iteration makes 3 requests. The
// defaults here stay under that. To push harder, start the gateway with a higher
// GATEWAY_RATE_RPS and GATEWAY_RATE_BURST, otherwise you are measuring the
// limiter, not the system.
import http from "k6/http";
import { check, sleep } from "k6";
import { Counter } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://localhost:8080";

// A seat taken by another virtual user (409) is an expected outcome under
// contention, so it does not count as a failed request.
http.setResponseCallback(http.expectedStatuses(200, 201, 409));

export const options = {
  vus: Number(__ENV.VUS || 2),
  duration: __ENV.DURATION || "30s",
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<2000"],
  },
};

const confirmed = new Counter("bookings_confirmed");
const seatTaken = new Counter("bookings_seat_taken");

function pick(items) {
  return items[Math.floor(Math.random() * items.length)];
}

export default function () {
  const date = new Date(Date.now() + 24 * 3600 * 1000).toISOString().slice(0, 10);

  const search = http.get(`${BASE}/v1/search?origin=PNQ&dest=BOM&date=${date}&mode=bus`);
  if (!check(search, { "search 200": (r) => r.status === 200 })) return;
  const trips = search.json("trips") || [];
  if (trips.length === 0) return;
  const trip = pick(trips);
  const cls = pick(trip.classes);

  const seats = http.get(`${BASE}/v1/trips/${trip.id}/seats?class=${cls.code}`);
  if (!check(seats, { "seats 200": (r) => r.status === 200 })) return;
  const free = seats.json("seat_nos") || [];
  if (free.length === 0) return;

  const res = http.post(
    `${BASE}/v1/bookings`,
    JSON.stringify({ trip_id: trip.id, seat_nos: [pick(free)], amount_paise: cls.fare_paise }),
    { headers: { "Content-Type": "application/json", Authorization: `Bearer k6-vu-${__VU}` } },
  );
  if (res.status === 201 && res.json("status") === "STATUS_CONFIRMED") {
    confirmed.add(1);
  } else if (res.status === 409) {
    seatTaken.add(1);
  }
  check(res, { "booking 201 or seat taken": (r) => r.status === 201 || r.status === 409 });

  sleep(1);
}
