-- name: LockSeats :many
-- Locks the requested seat rows for the duration of the caller's
-- transaction. Ordered by seat_no so two concurrent multi-seat holds that
-- overlap always attempt to acquire locks in the same order, which rules
-- out a lock-ordering deadlock between them.
SELECT trip_id, seat_no, class_code, status, hold_id, booking_id
FROM seats
WHERE trip_id = ?
  AND seat_no IN (sqlc.slice('seat_nos'))
ORDER BY seat_no
    FOR UPDATE;

-- name: MarkSeatsHeld :exec
UPDATE seats
SET status = 'held', hold_id = ?
WHERE trip_id = ?
  AND seat_no IN (sqlc.slice('seat_nos'));

-- name: MarkSeatsAvailableFromHold :execrows
-- Guarded by hold_id so a late release (e.g. a sweeper racing an explicit
-- confirm) can never clobber a seat that has since moved on to a new
-- hold or been booked.
UPDATE seats
SET status = 'available', hold_id = NULL
WHERE trip_id = ?
  AND seat_no IN (sqlc.slice('seat_nos'))
  AND hold_id = ?;

-- name: MarkSeatsBooked :execrows
UPDATE seats
SET status = 'booked', hold_id = NULL, booking_id = ?
WHERE trip_id = ?
  AND seat_no IN (sqlc.slice('seat_nos'))
  AND hold_id = ?;

-- name: MarkSeatsAvailableFromBooking :execrows
UPDATE seats
SET status = 'available', booking_id = NULL
WHERE trip_id = ?
  AND seat_no IN (sqlc.slice('seat_nos'))
  AND booking_id = ?;

-- name: GetSeat :one
SELECT trip_id, seat_no, class_code, status, hold_id, booking_id
FROM seats
WHERE trip_id = ? AND seat_no = ?;

-- name: ListAvailableSeats :many
-- Read-only lookup for callers (gateway) that need real seat numbers to
-- offer a user before calling CreateHold - not used anywhere on the
-- locking path itself.
SELECT seat_no
FROM seats
WHERE trip_id = ?
  AND class_code = ?
  AND status = 'available'
ORDER BY seat_no;
