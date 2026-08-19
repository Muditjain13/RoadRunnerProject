-- bookings: one row per booking attempt, tracking the saga's progress
-- through search(external) -> hold -> payment -> supplier confirm ->
-- ticket issued. status is the single source of truth for "what step is
-- this booking on"; the reconciliation sweeper resumes any booking whose
-- status hasn't advanced in too long by re-running the same idempotent
-- steps the synchronous path uses (see internal/booking/service).
CREATE TABLE bookings (
    id             VARCHAR(64) NOT NULL PRIMARY KEY,
    pnr            VARCHAR(16) NULL,
    supplier_pnr   VARCHAR(16) NULL,
    user_id        VARCHAR(64) NOT NULL,
    trip_id        VARCHAR(64) NOT NULL,
    mode           VARCHAR(16) NOT NULL,
    seat_nos       JSON        NOT NULL,
    amount_paise   BIGINT      NOT NULL,
    hold_id        VARCHAR(64) NULL,
    payment_id     VARCHAR(64) NULL,
    status         ENUM('pending', 'held', 'paid', 'confirmed', 'refunding', 'failed', 'cancelled')
                               NOT NULL DEFAULT 'pending',
    failure_reason VARCHAR(255) NULL,
    created_at     TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_bookings_pnr (pnr),
    -- Reconciliation sweeper's staleness query: WHERE status IN (...) AND updated_at < ?
    INDEX idx_bookings_status_updated (status, updated_at)
) ENGINE = InnoDB;

-- outbox_events: the transactional outbox. Every meaningful booking state
-- transition (confirmed, failed, cancelled) inserts a row here in the SAME
-- transaction as the bookings update - never a separate Kafka publish
-- call, per CLAUDE.md's "no dual writes" rule. A relay goroutine polls
-- published_at IS NULL, publishes to Kafka, then marks the row published.
CREATE TABLE outbox_events (
    id           BIGINT      NOT NULL AUTO_INCREMENT PRIMARY KEY,
    booking_id   VARCHAR(64) NOT NULL,
    event_type   VARCHAR(64) NOT NULL,
    payload      JSON        NOT NULL,
    published_at TIMESTAMP   NULL,
    created_at   TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Relay's poll query: WHERE published_at IS NULL ORDER BY id LIMIT ?
    INDEX idx_outbox_unpublished (published_at, id)
) ENGINE = InnoDB;
