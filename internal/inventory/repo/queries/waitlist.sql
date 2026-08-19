-- name: LockAvailableSeatsByClass :many
-- Locks up to `count` available seats in one class, ordered by seat_no -
-- the same lock-ordering discipline as LockSeats. Returns fewer than
-- `count` rows if fewer are available; CreateHoldByClass treats that as
-- "fall back to waitlist" rather than a partial hold.
SELECT seat_no
FROM seats
WHERE trip_id = ?
  AND class_code = ?
  AND status = 'available'
ORDER BY seat_no
LIMIT ?
    FOR UPDATE;

-- name: GetSeatClassCodes :many
-- Distinct class codes among the given seats - used after freeing seats to
-- know which waitlist queue(s), if any, to notify.
SELECT DISTINCT class_code
FROM seats
WHERE trip_id = ?
  AND seat_no IN (sqlc.slice('seat_nos'));

-- name: InsertWaitlistEntry :execlastid
INSERT INTO waitlist_entries (trip_id, class_code, user_id, seat_count, amount_paise, status)
VALUES (?, ?, ?, ?, ?, 'pending');

-- name: GetWaitlistHeadForUpdate :one
-- Locks the oldest pending entry for (trip_id, class_code), if any. The
-- promotion consumer's whole attempt runs inside this lock, so two
-- concurrent seats-freed events for the same (trip, class) can never
-- promote the same entry twice.
SELECT id, trip_id, class_code, user_id, seat_count, amount_paise, status, promoted_hold_id
FROM waitlist_entries
WHERE trip_id = ?
  AND class_code = ?
  AND status = 'pending'
ORDER BY id
LIMIT 1
    FOR UPDATE;

-- name: UpdateWaitlistPromoted :exec
UPDATE waitlist_entries
SET status = 'promoted', promoted_hold_id = ?
WHERE id = ?;

-- name: GetWaitlistEntry :one
SELECT id, trip_id, class_code, user_id, seat_count, amount_paise, status, promoted_hold_id
FROM waitlist_entries
WHERE id = ?;

-- name: ListWaitlistEntriesByUser :many
SELECT id, trip_id, class_code, user_id, seat_count, amount_paise, status, promoted_hold_id
FROM waitlist_entries
WHERE user_id = ?
ORDER BY id DESC;

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (trip_id, event_type, payload)
VALUES (?, ?, ?);

-- name: ListUnpublishedOutboxEvents :many
SELECT id, trip_id, event_type, payload
FROM outbox_events
WHERE published_at IS NULL
ORDER BY id
LIMIT ?;

-- name: MarkOutboxEventPublished :exec
UPDATE outbox_events SET published_at = CURRENT_TIMESTAMP WHERE id = ?;
