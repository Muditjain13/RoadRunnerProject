-- Backs GetBookingByHoldID: the idempotency check for creating a booking
-- directly from an inventory hold (train's atomic class-hold, or claiming
-- a promoted waitlist entry) - both flows must find "a booking already
-- exists for this hold" without a full table scan.
CREATE INDEX idx_bookings_hold_id ON bookings (hold_id);
