-- name: CreateHold :exec
INSERT INTO holds (id, trip_id, user_id, status, expires_at)
VALUES (?, ?, ?, 'active', ?);

-- name: InsertHoldSeat :exec
INSERT INTO hold_seats (hold_id, trip_id, seat_no)
VALUES (?, ?, ?);

-- name: GetHoldForUpdate :one
-- Locks the hold row so a concurrent sweeper sweep and an explicit
-- release/confirm of the same hold serialize instead of racing.
SELECT id, trip_id, user_id, status, expires_at
FROM holds
WHERE id = ?
    FOR UPDATE;

-- name: GetHoldSeats :many
SELECT trip_id, seat_no
FROM hold_seats
WHERE hold_id = ?
ORDER BY seat_no;

-- name: UpdateHoldStatus :exec
UPDATE holds
SET status = ?
WHERE id = ?;

-- name: ListExpiredActiveHolds :many
-- Sweeper query: batches of expired-but-still-active holds to release.
SELECT id, trip_id
FROM holds
WHERE status = 'active'
  AND expires_at < ?
ORDER BY expires_at
LIMIT ?;
