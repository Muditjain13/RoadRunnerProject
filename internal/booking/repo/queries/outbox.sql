-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (booking_id, event_type, payload)
VALUES (?, ?, ?);

-- name: ListUnpublishedOutboxEvents :many
SELECT id, booking_id, event_type, payload
FROM outbox_events
WHERE published_at IS NULL
ORDER BY id
LIMIT ?;

-- name: MarkOutboxEventPublished :exec
UPDATE outbox_events SET published_at = CURRENT_TIMESTAMP WHERE id = ?;
