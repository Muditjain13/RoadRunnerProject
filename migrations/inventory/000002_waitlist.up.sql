-- waitlist_entries: FIFO queue per (trip_id, class_code) for quota
-- exhaustion (train only in practice, but nothing here is train-specific -
-- bus class codes just never produce a pending row). amount_paise is
-- captured at join time so a later "pay now" doesn't need to re-derive the
-- fare from catalog. promoted_hold_id is set once a real inventory hold
-- exists for this entry; a booking can then be created from it.
CREATE TABLE waitlist_entries (
    id               BIGINT      NOT NULL AUTO_INCREMENT PRIMARY KEY,
    trip_id          VARCHAR(64) NOT NULL,
    class_code       VARCHAR(32) NOT NULL,
    user_id          VARCHAR(64) NOT NULL,
    seat_count       INT         NOT NULL,
    amount_paise     BIGINT      NOT NULL,
    status           ENUM('pending', 'promoted', 'cancelled') NOT NULL DEFAULT 'pending',
    promoted_hold_id VARCHAR(64) NULL,
    created_at       TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    -- Promotion consumer's query: oldest pending entry for a (trip, class).
    INDEX idx_waitlist_trip_class_status (trip_id, class_code, status, id)
) ENGINE = InnoDB;

-- outbox_events: transactional outbox mirroring booking's. Every seat
-- transition that frees capacity (hold release/expiry, booking
-- cancellation) inserts a row here in the SAME transaction as the seat
-- update. A relay publishes to Kafka; a bounded-worker-pool consumer
-- re-checks (trip_id, class_code) availability and promotes the waitlist
-- head if enough seats are now free. Harmless no-op for classes with no
-- pending entries (i.e. every bus class).
CREATE TABLE outbox_events (
    id           BIGINT      NOT NULL AUTO_INCREMENT PRIMARY KEY,
    trip_id      VARCHAR(64) NOT NULL,
    event_type   VARCHAR(64) NOT NULL,
    payload      JSON        NOT NULL,
    published_at TIMESTAMP   NULL,
    created_at   TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_outbox_unpublished (published_at, id)
) ENGINE = InnoDB;
