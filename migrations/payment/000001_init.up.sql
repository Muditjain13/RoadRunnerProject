-- payments: one row per charge attempt. idempotency_key is unique so a
-- retried Charge call (e.g. booking's saga retrying after a network
-- timeout) replays the recorded outcome instead of charging twice.
CREATE TABLE payments (
    id              VARCHAR(64) NOT NULL PRIMARY KEY,
    booking_id      VARCHAR(64) NOT NULL,
    user_id         VARCHAR(64) NOT NULL,
    amount_paise    BIGINT      NOT NULL,
    status          ENUM('succeeded', 'failed') NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    created_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_payments_idempotency_key (idempotency_key),
    INDEX idx_payments_booking_id (booking_id)
) ENGINE = InnoDB;

-- refunds: one row per refund attempt against a payment. A payment's
-- total refunded amount (sum of succeeded refunds) must never exceed its
-- amount_paise - enforced in the service layer under a row lock on the
-- parent payment, not by a DB constraint (MySQL has no CHECK across rows).
CREATE TABLE refunds (
    id              VARCHAR(64) NOT NULL PRIMARY KEY,
    payment_id      VARCHAR(64) NOT NULL,
    amount_paise    BIGINT      NOT NULL,
    status          ENUM('succeeded', 'failed') NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    created_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_refunds_idempotency_key (idempotency_key),
    INDEX idx_refunds_payment_id (payment_id),
    FOREIGN KEY (payment_id) REFERENCES payments (id)
) ENGINE = InnoDB;
