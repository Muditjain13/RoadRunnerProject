-- external_hold_ref carries a pre-payment hold reference from a supplier
-- that owns its own inventory (currently only flight - CLAUDE.md:
-- "Provider owns it... No local lock possible. External hold"). Distinct
-- from hold_id (an inventory hold, bus/train) and supplier_pnr (issued
-- only after payment, on ticketing/Confirm) - a flight booking has an
-- external_hold_ref from creation but no hold_id at all, ever.
ALTER TABLE bookings ADD COLUMN external_hold_ref VARCHAR(64) NULL;
