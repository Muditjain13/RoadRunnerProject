// RoadRunner demo frontend. Talks only to gateway's own REST API - no
// other backend knowledge lives here. Kept dependency-free (no build
// step) since this is a demo UI, not the system CLAUDE.md's architecture
// is centered on.

const CITIES = [
  { code: "DEL", name: "Delhi" },
  { code: "BOM", name: "Mumbai" },
  { code: "MAA", name: "Chennai" },
  { code: "BLR", name: "Bengaluru" },
  { code: "PNQ", name: "Pune" },
  { code: "JAI", name: "Jaipur" },
  { code: "GAU", name: "Guwahati" },
  { code: "SXR", name: "Srinagar" },
  { code: "AMD", name: "Ahmedabad" },
  { code: "IDR", name: "Indore" },
];

const state = {
  lastSearchResults: [],
  seatCache: new Map(), // tripId|classCode -> string[]
  pendingBooking: null, // {tripId, classCode, farePaise, seatCount, selectedSeats}
  pendingTrainBooking: null, // {trip, cls, availableCount}
  pendingFlightBooking: null, // {trip, cls}
  searchMode: "any", // "any" | "bus" | "train" | "flight"
};

// ---- storage helpers -------------------------------------------------

function getUser() {
  return localStorage.getItem("rr_user") || "";
}
function setUser(name) {
  localStorage.setItem("rr_user", name);
}
function clearUser() {
  localStorage.removeItem("rr_user");
}
function getMyBookings() {
  try {
    return JSON.parse(localStorage.getItem("rr_bookings") || "[]");
  } catch {
    return [];
  }
}
function addMyBooking(id) {
  const list = getMyBookings();
  if (!list.includes(id)) {
    list.unshift(id);
    localStorage.setItem("rr_bookings", JSON.stringify(list.slice(0, 20)));
  }
}

// ---- api helpers -------------------------------------------------------

async function api(path, opts = {}) {
  const headers = opts.headers || {};
  if (opts.auth) headers["Authorization"] = "Bearer " + encodeURIComponent(getUser());
  const res = await fetch(path, { ...opts, headers });
  let body = null;
  try {
    body = await res.json();
  } catch {
    // no body
  }
  if (!res.ok) {
    const err = new Error((body && body.error) || `request failed (${res.status})`);
    err.status = res.status;
    err.body = body;
    throw err;
  }
  return body;
}

async function searchTrips(origin, dest, date, mode) {
  const params = { origin, dest, date };
  if (mode && mode !== "any") params.mode = mode;
  const q = new URLSearchParams(params);
  return api(`/v1/search?${q}`);
}

async function listAvailableSeats(tripId, classCode) {
  const key = tripId + "|" + classCode;
  if (state.seatCache.has(key)) return state.seatCache.get(key);
  const resp = await api(`/v1/trips/${encodeURIComponent(tripId)}/seats?class=${encodeURIComponent(classCode)}`);
  const seats = resp.seat_nos || [];
  state.seatCache.set(key, seats);
  return seats;
}

async function createBooking(tripId, seatNos, amountPaise) {
  return api("/v1/bookings", {
    method: "POST",
    auth: true,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ trip_id: tripId, seat_nos: seatNos, amount_paise: amountPaise }),
  });
}

async function getBooking(id) {
  return api(`/v1/bookings/${encodeURIComponent(id)}`, { auth: true });
}

async function cancelBooking(id) {
  return api(`/v1/bookings/${encodeURIComponent(id)}/cancel`, { method: "POST", auth: true });
}

// createTrainBooking returns { outcome: "booked", booking } or
// { outcome: "waitlisted", waitlist_entry_id }.
async function createTrainBooking(tripId, classCode, seatCount, amountPaise) {
  return api("/v1/bookings/train", {
    method: "POST",
    auth: true,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ trip_id: tripId, class_code: classCode, seat_count: seatCount, amount_paise: amountPaise }),
  });
}

// createFlightBooking always returns a real booking or throws - flight
// has no waitlist outcome (the supplier's external hold either succeeds
// or fails, synchronously).
async function createFlightBooking(tripId, seatCount, amountPaise) {
  return api("/v1/bookings/flight", {
    method: "POST",
    auth: true,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ trip_id: tripId, seat_count: seatCount, amount_paise: amountPaise }),
  });
}

async function listMyWaitlist() {
  return api("/v1/waitlist", { auth: true });
}

async function claimWaitlistEntry(id, tripId, amountPaise) {
  return api(`/v1/waitlist/${encodeURIComponent(id)}/claim`, {
    method: "POST",
    auth: true,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ trip_id: tripId, amount_paise: amountPaise }),
  });
}

// ---- formatting ----------------------------------------------------------

function formatPaise(paise) {
  return "₹" + (paise / 100).toFixed(0);
}
function formatTime(iso) {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
function formatDateShort(iso) {
  const d = new Date(iso);
  return d.toLocaleDateString([], { day: "2-digit", month: "short" });
}
function formatDuration(startIso, endIso) {
  const mins = Math.round((new Date(endIso) - new Date(startIso)) / 60000);
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  return `${h}h ${m}m`;
}
// formatSeatNo strips the class-code prefix from a raw seat identifier
// (e.g. "seater-14" -> "Seat 14") for display only - the raw value is
// still what's sent to the booking API.
function formatSeatNo(seatNo) {
  const parts = String(seatNo).split("-");
  return "Seat " + parts[parts.length - 1];
}

function formatStatus(status) {
  return String(status || "")
    .replace(/^STATUS_/, "")
    .toLowerCase()
    .replace(/^./, (c) => c.toUpperCase());
}

function capitalize(s) {
  return String(s || "")
    .replace(/_/g, " ")
    .split(" ")
    .map((w) => (w ? w[0].toUpperCase() + w.slice(1) : w))
    .join(" ");
}

function formatMode(mode) {
  return String(mode || "")
    .replace(/^MODE_/, "")
    .toLowerCase()
    .replace(/^./, (c) => c.toUpperCase());
}

function cityName(code) {
  const c = CITIES.find((c) => c.code === code);
  return c ? c.name : code;
}

// ---- toast ----------------------------------------------------------------

let toastTimer = null;
function toast(msg, isError) {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.classList.toggle("error", !!isError);
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.hidden = true), 3500);
}

// ---- sign in ----------------------------------------------------------------

function renderUserBox() {
  const label = document.getElementById("userLabel");
  const signOut = document.getElementById("signOutBtn");
  const user = getUser();
  if (user) {
    label.innerHTML = `Hi, <strong>${escapeHtml(user)}</strong> &middot; <button class="link-btn" id="myBookingsBtn">my bookings</button> &middot; <button class="link-btn" id="myWaitlistBtn">my waitlist</button>`;
    signOut.hidden = false;
    document.getElementById("myBookingsBtn").onclick = openMyBookings;
    document.getElementById("myWaitlistBtn").onclick = openMyWaitlist;
  } else {
    label.textContent = "";
    signOut.hidden = true;
  }
}

// Resolved by the single, permanent signInForm submit listener below -
// using a form (not manual onclick/onkeydown wiring) means clicking
// Continue and pressing Enter both fire the same native "submit" event,
// which is more robust than duplicating that logic by hand.
let signInResolver = null;

document.getElementById("signInForm").addEventListener("submit", (e) => {
  e.preventDefault();
  const input = document.getElementById("signInName");
  const name = input.value.trim();
  if (!name) return;
  setUser(name);
  renderUserBox();
  document.getElementById("signInModal").hidden = true;
  if (signInResolver) {
    const resolve = signInResolver;
    signInResolver = null;
    resolve(name);
  }
});

function requireSignIn() {
  if (getUser()) return Promise.resolve(getUser());
  const modal = document.getElementById("signInModal");
  const input = document.getElementById("signInName");
  modal.hidden = false;
  input.value = "";
  input.focus();
  return new Promise((resolve) => {
    signInResolver = resolve;
  });
}

document.getElementById("signOutBtn").addEventListener("click", () => {
  clearUser();
  renderUserBox();
});

// ---- search ----------------------------------------------------------------

function populateCitySelects() {
  const origin = document.getElementById("origin");
  const dest = document.getElementById("dest");
  for (const c of CITIES) {
    origin.add(new Option(`${c.name} (${c.code})`, c.code));
    dest.add(new Option(`${c.name} (${c.code})`, c.code));
  }
  origin.value = "DEL";
  dest.value = "BOM";
}

document.getElementById("modeToggle").addEventListener("click", (e) => {
  const btn = e.target.closest(".mode-opt");
  if (!btn) return;
  state.searchMode = btn.dataset.mode;
  for (const opt of document.querySelectorAll(".mode-opt")) {
    opt.classList.toggle("active", opt === btn);
  }
});

document.getElementById("swapBtn").addEventListener("click", () => {
  const origin = document.getElementById("origin");
  const dest = document.getElementById("dest");
  const tmp = origin.value;
  origin.value = dest.value;
  dest.value = tmp;
});

function setDefaultDate() {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  document.getElementById("date").value = d.toISOString().slice(0, 10);
  document.getElementById("date").min = new Date().toISOString().slice(0, 10);
}

document.getElementById("searchForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const origin = document.getElementById("origin").value;
  const dest = document.getElementById("dest").value;
  const date = document.getElementById("date").value;

  if (origin === dest) {
    toast("Origin and destination can't be the same city", true);
    return;
  }

  const mode = state.searchMode;
  const modeLabel = mode === "bus" ? "buses" : mode === "train" ? "trains" : mode === "flight" ? "flights" : "trips";

  const statusArea = document.getElementById("statusArea");
  const resultsArea = document.getElementById("resultsArea");
  resultsArea.hidden = true;
  statusArea.hidden = false;
  statusArea.className = "status-area";
  statusArea.innerHTML = `<div class="spinner"></div>Searching ${modeLabel} from ${cityName(origin)} to ${cityName(dest)}&hellip;`;

  try {
    const resp = await searchTrips(origin, dest, date, mode);
    statusArea.hidden = true;
    renderResults(resp, origin, dest, date, mode);
  } catch (err) {
    statusArea.className = "status-area error";
    statusArea.textContent = "Search failed: " + err.message;
  }
});

function renderResults(resp, origin, dest, date, mode) {
  const resultsArea = document.getElementById("resultsArea");
  const title = document.getElementById("resultsTitle");
  const list = document.getElementById("resultsList");
  const degradedNotice = document.getElementById("degradedNotice");

  resultsArea.hidden = false;
  const trips = resp.trips || [];
  const noun =
    mode === "bus" ? (trips.length === 1 ? "bus" : "buses") :
    mode === "train" ? `train${trips.length === 1 ? "" : "s"}` :
    mode === "flight" ? `flight${trips.length === 1 ? "" : "s"}` :
    `trip${trips.length === 1 ? "" : "s"}`;
  title.textContent = `${trips.length} ${noun} from ${cityName(origin)} to ${cityName(dest)}, ${formatDateShort(date + "T00:00:00Z")}`;

  if (resp.degraded_providers && resp.degraded_providers.length > 0) {
    degradedNotice.hidden = false;
    degradedNotice.textContent = `Some providers didn't respond in time (${resp.degraded_providers.map(formatMode).join(", ")}) - showing partial results.`;
  } else {
    degradedNotice.hidden = true;
  }

  list.innerHTML = "";
  if (trips.length === 0) {
    list.innerHTML = `<div class="empty-state"><div class="big">&#128683;</div>No trips found on this route for this date.<br>Try a different date, or note that not every route has bus and train service (e.g. Srinagar is bus-only, long-haul routes are flight only).</div>`;
    return;
  }

  for (const trip of trips) {
    list.appendChild(renderTripCard(trip));
  }
}

function renderTripCard(trip) {
  const card = document.createElement("div");
  card.className = "trip-card";
  const isTrain = trip.mode === "MODE_TRAIN";
  const isFlight = trip.mode === "MODE_FLIGHT";
  const modeClass = isFlight ? "flight" : isTrain ? "train" : "bus";
  const modeLabel = isFlight ? "Flight" : isTrain ? "Train" : "Bus";

  const opName = trip.operator_name || trip.operator_id || "Operator";
  card.innerHTML = `
    <div class="trip-top">
      <div class="trip-op"><span class="mode-badge ${modeClass}">${modeLabel}</span> ${escapeHtml(opName)}</div>
      <div class="trip-vehicle">${escapeHtml(trip.vehicle_number || "")}</div>
    </div>
    <div class="trip-times">
      <div><span class="time">${formatTime(trip.departure_at)}</span><span class="date-sub">departs</span></div>
      <div class="duration">${formatDuration(trip.departure_at, trip.arrival_at)}</div>
      <div><span class="time">${formatTime(trip.arrival_at)}</span><span class="date-sub">arrives</span></div>
    </div>
    <div class="classes-row"></div>
  `;

  const classesRow = card.querySelector(".classes-row");
  for (const cls of trip.classes || []) {
    const chip = document.createElement("div");
    chip.className = "class-chip";
    chip.innerHTML = `
      <div class="cls-name">${escapeHtml(capitalize(cls.code))}</div>
      <div class="cls-fare">${formatPaise(cls.fare_paise)}</div>
      <div class="cls-seats">checking&hellip;</div>
    `;
    classesRow.appendChild(chip);

    if (isFlight) {
      // Flight has no local inventory to check (CLAUDE.md: "Provider
      // owns it") - there is no live seat count to query. The supplier's
      // external hold, made when the booking is actually attempted, is
      // the only real availability check.
      chip.querySelector(".cls-seats").textContent = `${cls.capacity} seats`;
      chip.addEventListener("click", () => openFlightBookingModal(trip, cls));
      continue;
    }

    listAvailableSeats(trip.id, cls.code)
      .then((seats) => {
        const seatsEl = chip.querySelector(".cls-seats");
        if (isTrain) {
          // Train quotas can run out - unlike bus, a full class is still
          // clickable, since requesting seats on it is exactly what
          // produces a waitlist entry instead of a hold.
          seatsEl.textContent = seats.length === 0 ? "waitlist only" : `${seats.length} left`;
          chip.addEventListener("click", () => openTrainBookingModal(trip, cls, seats.length));
        } else if (seats.length === 0) {
          seatsEl.textContent = "sold out";
          chip.classList.add("sold-out");
        } else {
          seatsEl.textContent = `${seats.length} left`;
          chip.addEventListener("click", () => openBookingModal(trip, cls, seats));
        }
      })
      .catch(() => {
        chip.querySelector(".cls-seats").textContent = "unavailable";
        chip.classList.add("sold-out");
      });
  }

  return card;
}

// ---- booking modal ----------------------------------------------------------------

function openBookingModal(trip, cls, availableSeats) {
  state.pendingBooking = { trip, cls, availableSeats, selected: [] };
  renderSeatPicker();
  document.getElementById("bookingModal").hidden = false;
}

function renderSeatPicker() {
  const { trip, cls, availableSeats, selected } = state.pendingBooking;
  const body = document.getElementById("modalBody");
  const maxPick = Math.min(availableSeats.length, 6);

  body.innerHTML = `
    <h3>${escapeHtml(capitalize(cls.code))} &middot; ${formatTime(trip.departure_at)} &rarr; ${formatTime(trip.arrival_at)}</h3>
    <p class="muted">Pick up to ${maxPick} seat${maxPick === 1 ? "" : "s"}.</p>
    <div class="seat-grid" id="seatGrid"></div>
    <div class="summary-row"><span>Fare per seat</span><span>${formatPaise(cls.fare_paise)}</span></div>
    <div class="summary-row"><span>Seats selected</span><span id="seatCountLabel">0</span></div>
    <div class="summary-row total"><span>Total</span><span id="totalLabel">${formatPaise(0)}</span></div>
    <button class="primary-btn" id="confirmBookBtn" style="width:100%;margin-top:14px" disabled>Select at least one seat</button>
  `;

  const grid = document.getElementById("seatGrid");
  for (const seatNo of availableSeats.slice(0, 24)) {
    const chip = document.createElement("div");
    chip.className = "seat-chip";
    chip.textContent = seatNo.split("-").pop();
    chip.addEventListener("click", () => toggleSeat(seatNo, chip, maxPick));
    grid.appendChild(chip);
  }

  document.getElementById("confirmBookBtn").addEventListener("click", confirmBooking);
}

function toggleSeat(seatNo, chip, maxPick) {
  const b = state.pendingBooking;
  const idx = b.selected.indexOf(seatNo);
  if (idx >= 0) {
    b.selected.splice(idx, 1);
    chip.classList.remove("selected");
  } else {
    if (b.selected.length >= maxPick) {
      toast(`You can select up to ${maxPick} seats`, true);
      return;
    }
    b.selected.push(seatNo);
    chip.classList.add("selected");
  }
  const count = b.selected.length;
  document.getElementById("seatCountLabel").textContent = count;
  document.getElementById("totalLabel").textContent = formatPaise(count * b.cls.fare_paise);
  const btn = document.getElementById("confirmBookBtn");
  btn.disabled = count === 0;
  btn.textContent = count === 0 ? "Select at least one seat" : `Confirm & pay ${formatPaise(count * b.cls.fare_paise)}`;
}

async function confirmBooking() {
  const b = state.pendingBooking;
  if (!b || b.selected.length === 0) return;

  await requireSignIn();

  const btn = document.getElementById("confirmBookBtn");
  btn.disabled = true;
  btn.textContent = "Booking…";

  const amountPaise = b.selected.length * b.cls.fare_paise;
  try {
    // The API returns the booking object directly (no envelope).
    const booking = await createBooking(b.trip.id, b.selected, amountPaise);
    if (booking && booking.id) addMyBooking(booking.id);
    renderBookingResult(booking);
    // Free the seats we just possibly consumed from the cached snapshot
    // so the results list re-checks live availability on next open.
    state.seatCache.delete(b.trip.id + "|" + b.cls.code);
  } catch (err) {
    renderBookingError(err);
  }
}

function statusBadge(status) {
  // CSS rules are keyed on the bare word (e.g. .status-badge.confirmed),
  // not the raw "STATUS_CONFIRMED" enum string.
  const cssClass = String(status || "").replace(/^STATUS_/, "").toLowerCase();
  return `<span class="status-badge ${cssClass}">${escapeHtml(formatStatus(status))}</span>`;
}

function renderBookingResult(booking) {
  const body = document.getElementById("modalBody");
  const status = booking.status;
  const isConfirmed = status === "STATUS_CONFIRMED";
  const isBad = status === "STATUS_FAILED" || status === "STATUS_CANCELLED";

  let explanation = "";
  if (!isConfirmed && !isBad) {
    explanation = `<p class="muted">Your booking is still being processed (${status}). This can happen if a downstream step is momentarily slow - the system will automatically finish or roll it back. Check "my bookings" shortly.</p>`;
  }
  if (isBad && booking.failure_reason) {
    explanation = `<p class="muted">${escapeHtml(booking.failure_reason)}</p>`;
  }

  body.innerHTML = `
    <div class="booking-result">
      <div class="icon">${isConfirmed ? "✅" : isBad ? "⚠️" : "⏳"}</div>
      <h3>${isConfirmed ? "Booking confirmed!" : isBad ? "Booking didn't go through" : "Booking in progress"}</h3>
      ${statusBadge(status)}
      ${
        booking.pnr
          ? `<div class="pnr-box">${escapeHtml(booking.pnr)}</div>`
          : ""
      }
      ${explanation}
      <p class="muted">${escapeHtml((booking.seat_nos || []).map(formatSeatNo).join(", "))}</p>
    </div>
  `;
}

function renderBookingError(err) {
  const body = document.getElementById("modalBody");
  body.innerHTML = `
    <div class="booking-result">
      <div class="icon">&#10060;</div>
      <h3>Couldn't complete booking</h3>
      <p class="muted">${escapeHtml(err.message)}</p>
    </div>
  `;
}

document.getElementById("modalCloseBtn").addEventListener("click", () => {
  document.getElementById("bookingModal").hidden = true;
  state.pendingBooking = null;
  state.pendingTrainBooking = null;
  state.pendingFlightBooking = null;
});
document.getElementById("bookingModal").addEventListener("click", (e) => {
  if (e.target.id === "bookingModal") {
    document.getElementById("bookingModal").hidden = true;
  }
});

// ---- train booking modal ----------------------------------------------------------------
// Train differs from bus here: the caller names a class and a seat count,
// not explicit seats - inventory itself picks which seats (or queues a
// waitlist entry if the class is exhausted). There is deliberately no
// seat grid for train.

function formatWaitlistStatus(status) {
  return String(status || "")
    .replace(/^WAITLIST_STATUS_/, "")
    .toLowerCase()
    .replace(/^./, (c) => c.toUpperCase());
}

function openTrainBookingModal(trip, cls, availableCount) {
  state.pendingTrainBooking = { trip, cls, availableCount };
  renderTrainBookingForm();
  document.getElementById("bookingModal").hidden = false;
}

function renderTrainBookingForm() {
  const { trip, cls, availableCount } = state.pendingTrainBooking;
  const body = document.getElementById("modalBody");
  const maxPick = 6;

  body.innerHTML = `
    <h3>${escapeHtml(capitalize(cls.code))} &middot; ${formatTime(trip.departure_at)} &rarr; ${formatTime(trip.arrival_at)}</h3>
    <p class="muted">${availableCount === 0 ? "This class is fully booked right now - you can still join the waitlist." : `${availableCount} seat${availableCount === 1 ? "" : "s"} available. Inventory assigns your exact seats.`}</p>
    <div class="field">
      <label for="trainSeatCount">How many seats?</label>
      <input type="number" id="trainSeatCount" min="1" max="${maxPick}" value="1">
    </div>
    <div class="summary-row"><span>Fare per seat</span><span>${formatPaise(cls.fare_paise)}</span></div>
    <div class="summary-row total"><span>Total</span><span id="trainTotalLabel">${formatPaise(cls.fare_paise)}</span></div>
    <button class="primary-btn" id="confirmTrainBookBtn" style="width:100%;margin-top:14px">Confirm &amp; pay ${formatPaise(cls.fare_paise)}</button>
  `;

  const countInput = document.getElementById("trainSeatCount");
  const totalLabel = document.getElementById("trainTotalLabel");
  const btn = document.getElementById("confirmTrainBookBtn");
  const updateTotal = () => {
    const n = Math.max(1, Math.min(maxPick, parseInt(countInput.value, 10) || 1));
    countInput.value = n;
    const total = formatPaise(n * cls.fare_paise);
    totalLabel.textContent = total;
    btn.textContent = `Confirm & pay ${total}`;
  };
  countInput.addEventListener("input", updateTotal);
  btn.addEventListener("click", () => confirmTrainBooking(parseInt(countInput.value, 10) || 1));
}

async function confirmTrainBooking(seatCount) {
  const b = state.pendingTrainBooking;
  if (!b) return;

  await requireSignIn();

  const btn = document.getElementById("confirmTrainBookBtn");
  btn.disabled = true;
  btn.textContent = "Booking…";

  const amountPaise = seatCount * b.cls.fare_paise;
  try {
    const result = await createTrainBooking(b.trip.id, b.cls.code, seatCount, amountPaise);
    if (result.outcome === "booked") {
      if (result.booking && result.booking.id) addMyBooking(result.booking.id);
      renderBookingResult(result.booking);
    } else {
      renderWaitlistedResult(result.waitlist_entry_id, b.cls);
    }
    state.seatCache.delete(b.trip.id + "|" + b.cls.code);
  } catch (err) {
    renderBookingError(err);
  }
}

function renderWaitlistedResult(waitlistEntryId, cls) {
  const body = document.getElementById("modalBody");
  body.innerHTML = `
    <div class="booking-result">
      <div class="icon">&#8987;</div>
      <h3>You're on the waitlist</h3>
      <p class="muted">${escapeHtml(capitalize(cls.code))} is full right now. We'll hold a spot for you (waitlist entry #${waitlistEntryId}) and free seats go to the earliest waitlisted request first.</p>
      <p class="muted">Check <button class="link-btn" id="goToWaitlistBtn">my waitlist</button> to pay once a seat opens up.</p>
    </div>
  `;
  document.getElementById("goToWaitlistBtn").addEventListener("click", () => {
    document.getElementById("bookingModal").hidden = true;
    openMyWaitlist();
  });
}

// ---- flight booking modal ----------------------------------------------------------------
// Flight differs from both bus and train: no seat grid (like train) and
// no live availability check (unlike both) - the provider owns the
// inventory, so the external hold made on confirm IS the availability
// check, not something queried beforehand.

function openFlightBookingModal(trip, cls) {
  state.pendingFlightBooking = { trip, cls };
  renderFlightBookingForm();
  document.getElementById("bookingModal").hidden = false;
}

function renderFlightBookingForm() {
  const { trip, cls } = state.pendingFlightBooking;
  const body = document.getElementById("modalBody");
  const maxPick = 6;

  body.innerHTML = `
    <h3>${escapeHtml(capitalize(cls.code))} &middot; ${formatTime(trip.departure_at)} &rarr; ${formatTime(trip.arrival_at)}</h3>
    <p class="muted">The airline confirms your seats when you book - there's no local seat count to check beforehand.</p>
    <div class="field">
      <label for="flightSeatCount">How many passengers?</label>
      <input type="number" id="flightSeatCount" min="1" max="${maxPick}" value="1">
    </div>
    <div class="summary-row"><span>Fare per passenger</span><span>${formatPaise(cls.fare_paise)}</span></div>
    <div class="summary-row total"><span>Total</span><span id="flightTotalLabel">${formatPaise(cls.fare_paise)}</span></div>
    <button class="primary-btn" id="confirmFlightBookBtn" style="width:100%;margin-top:14px">Confirm &amp; pay ${formatPaise(cls.fare_paise)}</button>
  `;

  const countInput = document.getElementById("flightSeatCount");
  const totalLabel = document.getElementById("flightTotalLabel");
  const btn = document.getElementById("confirmFlightBookBtn");
  const updateTotal = () => {
    const n = Math.max(1, Math.min(maxPick, parseInt(countInput.value, 10) || 1));
    countInput.value = n;
    const total = formatPaise(n * cls.fare_paise);
    totalLabel.textContent = total;
    btn.textContent = `Confirm & pay ${total}`;
  };
  countInput.addEventListener("input", updateTotal);
  btn.addEventListener("click", () => confirmFlightBooking(parseInt(countInput.value, 10) || 1));
}

async function confirmFlightBooking(seatCount) {
  const b = state.pendingFlightBooking;
  if (!b) return;

  await requireSignIn();

  const btn = document.getElementById("confirmFlightBookBtn");
  btn.disabled = true;
  btn.textContent = "Booking…";

  const amountPaise = seatCount * b.cls.fare_paise;
  try {
    const booking = await createFlightBooking(b.trip.id, seatCount, amountPaise);
    if (booking && booking.id) addMyBooking(booking.id);
    renderBookingResult(booking);
  } catch (err) {
    renderBookingError(err);
  }
}

// ---- my waitlist ----------------------------------------------------------------

async function openMyWaitlist() {
  await requireSignIn();
  const modal = document.getElementById("bookingModal");
  const body = document.getElementById("modalBody");
  body.innerHTML = `<h3>My waitlist</h3><div id="myWaitlistList"><div class="spinner"></div></div>`;
  modal.hidden = false;

  const list = document.getElementById("myWaitlistList");
  try {
    const resp = await listMyWaitlist();
    const entries = resp.entries || [];
    if (entries.length === 0) {
      list.innerHTML = `<p class="muted">No waitlist entries.</p>`;
      return;
    }
    list.innerHTML = "";
    for (const entry of entries) {
      list.appendChild(renderWaitlistRow(entry));
    }
  } catch (err) {
    list.innerHTML = `<p class="muted">${escapeHtml(err.message)}</p>`;
  }
}

function renderWaitlistRow(entry) {
  const row = document.createElement("div");
  row.className = "trip-card";
  const isPromoted = entry.status === "WAITLIST_STATUS_PROMOTED";
  row.innerHTML = `
    <div class="trip-top">
      <div class="trip-op">${escapeHtml(capitalize(entry.class_code))} &middot; ${entry.seat_count} seat${entry.seat_count === 1 ? "" : "s"}</div>
      <span class="status-badge ${isPromoted ? "confirmed" : ""}">${escapeHtml(formatWaitlistStatus(entry.status))}</span>
    </div>
    <p class="muted" style="margin:8px 0 0">${escapeHtml(entry.trip_id)} &middot; ${formatPaise(entry.amount_paise)}</p>
    ${isPromoted ? `<button class="primary-btn pay-now-btn" data-id="${entry.id}" data-trip="${escapeHtml(entry.trip_id)}" data-amount="${entry.amount_paise}" style="margin-top:10px">Pay now</button>` : `<p class="muted" style="margin-top:6px">Waiting for a seat to free up&hellip;</p>`}
  `;
  const payBtn = row.querySelector(".pay-now-btn");
  if (payBtn) {
    payBtn.addEventListener("click", async () => {
      payBtn.disabled = true;
      payBtn.textContent = "Paying…";
      try {
        const booking = await claimWaitlistEntry(payBtn.dataset.id, payBtn.dataset.trip, parseInt(payBtn.dataset.amount, 10));
        if (booking && booking.id) addMyBooking(booking.id);
        document.getElementById("modalBody").innerHTML = "";
        renderBookingResult(booking);
      } catch (err) {
        toast("Payment failed: " + err.message, true);
        payBtn.disabled = false;
        payBtn.textContent = "Pay now";
      }
    });
  }
  return row;
}

// ---- my bookings ----------------------------------------------------------------

async function openMyBookings() {
  const modal = document.getElementById("bookingModal");
  const body = document.getElementById("modalBody");
  const ids = getMyBookings();
  body.innerHTML = `<h3>My bookings</h3>` + (ids.length === 0 ? `<p class="muted">No bookings yet.</p>` : `<div id="myBookingsList" class="results-list"></div>`);
  modal.hidden = false;
  if (ids.length === 0) return;

  const list = document.getElementById("myBookingsList");
  for (const id of ids) {
    const row = document.createElement("div");
    row.className = "trip-card";
    row.innerHTML = `<div class="trip-top"><div class="trip-op">${id}</div><div>loading&hellip;</div></div>`;
    list.appendChild(row);
    try {
      const b = await getBooking(id);
      row.innerHTML = `
        <div class="trip-top">
          <div class="trip-op">${b.pnr ? "PNR " + escapeHtml(b.pnr) : escapeHtml(id.slice(0, 8))}</div>
          ${statusBadge(b.status)}
        </div>
        <p class="muted" style="margin:8px 0 0">${escapeHtml((b.seat_nos || []).map(formatSeatNo).join(", "))} &middot; ${formatPaise(b.amount_paise)}</p>
        ${b.status === "STATUS_CONFIRMED" ? `<button class="link-btn cancel-btn" data-id="${id}">cancel booking</button>` : ""}
      `;
      const cancelBtn = row.querySelector(".cancel-btn");
      if (cancelBtn) {
        cancelBtn.addEventListener("click", async () => {
          cancelBtn.textContent = "cancelling…";
          try {
            await cancelBooking(id);
            toast("Booking cancelled and refunded");
            openMyBookings();
          } catch (err) {
            toast("Cancel failed: " + err.message, true);
          }
        });
      }
    } catch (err) {
      row.innerHTML = `<div class="trip-top"><div class="trip-op">${id.slice(0, 8)}</div></div><p class="muted">${escapeHtml(err.message)}</p>`;
    }
  }
}

// ---- utils ----------------------------------------------------------------

function escapeHtml(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// ---- global error visibility ----------------------------------------------------------------
// A silently swallowed exception anywhere above looks identical to "the
// button did nothing" - surface it instead of leaving that ambiguous.

window.addEventListener("error", (e) => {
  console.error(e.error || e.message);
  toast("Unexpected error: " + (e.message || "see console"), true);
});
window.addEventListener("unhandledrejection", (e) => {
  console.error(e.reason);
  toast("Unexpected error: " + (e.reason && e.reason.message ? e.reason.message : "see console"), true);
});

// ---- init ----------------------------------------------------------------

populateCitySelects();
setDefaultDate();
renderUserBox();
