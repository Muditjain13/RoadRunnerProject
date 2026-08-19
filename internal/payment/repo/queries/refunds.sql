-- name: GetRefundByIdempotencyKey :one
SELECT id, payment_id, amount_paise, status, idempotency_key
FROM refunds
WHERE idempotency_key = ?;

-- name: InsertRefund :exec
INSERT INTO refunds (id, payment_id, amount_paise, status, idempotency_key)
VALUES (?, ?, ?, ?, ?);

-- name: SumSucceededRefunds :one
-- Must be called within the same transaction that holds
-- GetPaymentForUpdate's row lock, so no concurrent refund can be inserted
-- between this sum and the caller's over-refund check.
SELECT CAST(COALESCE(SUM(amount_paise), 0) AS SIGNED) AS total
FROM refunds
WHERE payment_id = ?
  AND status = 'succeeded';
