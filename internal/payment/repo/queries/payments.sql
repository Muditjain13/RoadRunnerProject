-- name: GetPaymentByIdempotencyKey :one
SELECT id, booking_id, user_id, amount_paise, status, idempotency_key
FROM payments
WHERE idempotency_key = ?;

-- name: InsertPayment :exec
INSERT INTO payments (id, booking_id, user_id, amount_paise, status, idempotency_key)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetPaymentForUpdate :one
-- Locks the payment row so concurrent refund attempts against it
-- serialize - required to enforce "total refunded <= amount_paise"
-- without a race between the sum-check and the refund insert.
SELECT id, booking_id, user_id, amount_paise, status, idempotency_key
FROM payments
WHERE id = ?
    FOR UPDATE;

-- name: GetPayment :one
SELECT id, booking_id, user_id, amount_paise, status, idempotency_key
FROM payments
WHERE id = ?;
