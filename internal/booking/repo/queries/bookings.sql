-- name: InsertBooking :exec
INSERT INTO bookings (id, user_id, trip_id, mode, seat_nos, amount_paise, status)
VALUES (?, ?, ?, ?, ?, ?, 'pending');

-- name: InsertHeldBooking :exec
-- Used only when a booking is created directly at the Held step - train
-- mode's atomic class-hold, claiming a promoted waitlist entry, or
-- flight's external hold - where the hold (inventory or supplier-side)
-- already exists before the booking row does, unlike the normal
-- pending -> held path every other booking takes. Exactly one of
-- hold_id (inventory, bus/train) / external_hold_ref (supplier, flight)
-- is set; the other is NULL.
INSERT INTO bookings (id, user_id, trip_id, mode, seat_nos, amount_paise, hold_id, external_hold_ref, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'held');

-- name: GetBookingByHoldID :one
-- Idempotency check: has a booking already been created from this hold?
-- Used by CreateBookingFromWaitlist so claiming (and retrying the claim)
-- the same promotion never creates two bookings for one hold.
SELECT id, pnr, supplier_pnr, user_id, trip_id, mode, seat_nos, amount_paise,
       hold_id, external_hold_ref, payment_id, status, failure_reason
FROM bookings
WHERE hold_id = ?;

-- name: GetBooking :one
SELECT id, pnr, supplier_pnr, user_id, trip_id, mode, seat_nos, amount_paise,
       hold_id, external_hold_ref, payment_id, status, failure_reason
FROM bookings
WHERE id = ?;

-- name: GetBookingForUpdate :one
-- Locks the booking row so the synchronous saga path and the
-- reconciliation sweeper can never both advance the same booking at once.
SELECT id, pnr, supplier_pnr, user_id, trip_id, mode, seat_nos, amount_paise,
       hold_id, external_hold_ref, payment_id, status, failure_reason
FROM bookings
WHERE id = ?
    FOR UPDATE;

-- name: UpdateBookingHeld :exec
UPDATE bookings SET status = 'held', hold_id = ? WHERE id = ?;

-- name: UpdateBookingPaid :exec
UPDATE bookings SET status = 'paid', payment_id = ? WHERE id = ?;

-- name: UpdateBookingSupplierPNR :exec
-- Records the supplier PNR without changing status - called between the
-- supplier confirm and local inventory confirm sub-steps, so a resumed
-- booking knows a supplier PNR already exists and must be cancelled on
-- compensation, without re-confirming with the supplier.
UPDATE bookings SET supplier_pnr = ? WHERE id = ?;

-- name: UpdateBookingConfirmed :exec
UPDATE bookings SET status = 'confirmed', pnr = ? WHERE id = ?;

-- name: UpdateBookingRefunding :exec
UPDATE bookings SET status = 'refunding', failure_reason = ? WHERE id = ?;

-- name: UpdateBookingFailed :exec
UPDATE bookings SET status = 'failed', failure_reason = ? WHERE id = ?;

-- name: UpdateBookingCancelled :exec
UPDATE bookings SET status = 'cancelled' WHERE id = ?;

-- name: ListStaleBookings :many
-- Reconciliation sweeper's candidate list: bookings sitting in a
-- non-terminal status past updated_before, i.e. they haven't advanced in
-- too long and may be stuck mid-saga (crash, timeout, slow retry).
SELECT id
FROM bookings
WHERE status IN (sqlc.slice('statuses'))
  AND updated_at < ?
ORDER BY updated_at
LIMIT ?;
