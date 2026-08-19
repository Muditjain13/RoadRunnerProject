-- booking_events: a high-volume historical log of booking lifecycle
-- events (CLAUDE.md section 9: ~20,000,000 rows), distinct from
-- outbox_events (the small, operational, transactional-outbox table).
-- This table backs reporting/audit-style queries scoped to a date range
-- (e.g. "events created last month") - exactly the access pattern monthly
-- partitioning is meant to speed up. Left unpartitioned here deliberately;
-- migration 000004 adds partitioning after seeding, so the "before" state
-- can be measured honestly instead of assumed. created_at must be part of
-- every unique key for MySQL to allow partitioning by it later, hence the
-- composite primary key instead of a bare auto-increment id.
CREATE TABLE booking_events (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    booking_id VARCHAR(64) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    created_at DATETIME    NOT NULL,
    PRIMARY KEY (id, created_at)
) ENGINE = InnoDB;
