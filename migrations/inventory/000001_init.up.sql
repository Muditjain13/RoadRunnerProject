-- Seats: one row per physical seat on one trip. Created by the seeder (or,
-- in a real system, whatever consumes catalog's "trip created" event) once
-- per trip, then only ever transitions status via inventory's locking
-- service methods. PK is (trip_id, seat_no), which is exactly the row a
-- hold attempt must lock.
CREATE TABLE seats (
    trip_id    VARCHAR(64)  NOT NULL,
    seat_no    VARCHAR(16)  NOT NULL,
    class_code VARCHAR(32)  NOT NULL,
    status     ENUM('available', 'held', 'booked') NOT NULL DEFAULT 'available',
    hold_id    VARCHAR(64)  NULL,
    booking_id VARCHAR(64)  NULL,
    created_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (trip_id, seat_no),
    INDEX idx_seats_hold_id (hold_id),
    INDEX idx_seats_booking_id (booking_id)
) ENGINE = InnoDB;

-- Holds: one row per hold attempt (a hold can cover several seats for a
-- multi-passenger booking). TTL is expires_at; the sweeper polls for
-- active holds past expiry and releases their seats.
CREATE TABLE holds (
    id         VARCHAR(64) NOT NULL PRIMARY KEY,
    trip_id    VARCHAR(64) NOT NULL,
    user_id    VARCHAR(64) NOT NULL,
    status     ENUM('active', 'expired', 'released', 'confirmed') NOT NULL DEFAULT 'active',
    expires_at TIMESTAMP   NOT NULL,
    created_at TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    -- Sweeper query: WHERE status = 'active' AND expires_at < ?
    INDEX idx_holds_status_expires (status, expires_at)
) ENGINE = InnoDB;

-- hold_seats: which seats belong to a hold. Split out (rather than a seat
-- list column on holds) so it can carry the same (trip_id, seat_no) FK
-- that seats uses, and so a hold's seat count is just a row count.
CREATE TABLE hold_seats (
    hold_id VARCHAR(64) NOT NULL,
    trip_id VARCHAR(64) NOT NULL,
    seat_no VARCHAR(16) NOT NULL,
    PRIMARY KEY (hold_id, trip_id, seat_no),
    FOREIGN KEY (hold_id) REFERENCES holds (id),
    FOREIGN KEY (trip_id, seat_no) REFERENCES seats (trip_id, seat_no)
) ENGINE = InnoDB;
