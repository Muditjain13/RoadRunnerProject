# Covering index for seat availability

## Query

`ListAvailableSeats` and `LockAvailableSeatsByClass` (inventory) both run:

```sql
SELECT seat_no FROM seats
WHERE trip_id = ? AND class_code = ? AND status = 'available'
ORDER BY seat_no;
```

This is the query the frontend fires for every class chip on every trip
card after a search, and the one `CreateHoldByClass` uses to lock seats
for a train hold-or-waitlist decision - a hot path.

## Setup

`seats` seeded to **5,729,820 rows** (`make seed` full profile: 90 days
forward, 1 trip/day, 90 routes, bus + train). Before this change, `seats`
had only its primary key, `(trip_id, seat_no)`, plus two low-cardinality
secondary indexes (`hold_id`, `booking_id`) unrelated to this query.

Representative row picked from the seeded data: trip
`train-BLR-AMD-20260916T0500`, class `sl`, 648 seats out of 1224 total
seats on that trip (the other 576 are `3a`/`2a`).

## Before: primary key only

```
EXPLAIN
SELECT seat_no FROM seats
WHERE trip_id='train-BLR-AMD-20260916T0500' AND class_code='sl' AND status='available'
ORDER BY seat_no;
```

| id | select_type | table | type | key | key_len | rows | filtered | Extra |
|---|---|---|---|---|---|---|---|---|
| 1 | SIMPLE | seats | ref | PRIMARY | 258 | 1224 | 3.33 | Using where |

The primary key narrows to one `trip_id` and happens to already return
rows in `seat_no` order (no filesort), but MySQL must still fetch and
examine **every seat row on the trip** - all three classes - to decide
which ones match `class_code`/`status`. Confirmed with handler counters
on a single execution:

```
FLUSH STATUS;
SELECT ...;
SHOW SESSION STATUS WHERE Variable_name IN
  ('Handler_read_key','Handler_read_next','Handler_read_rnd_next');
```

| Handler_read_key | Handler_read_next | Handler_read_rnd_next |
|---|---|---|
| 1 | 1224 | 0 |

1224 rows read to return 648 - 47% wasted reads, and every one of those
reads touches the full clustered-index row (seat status, hold_id,
booking_id, timestamps), not just the three columns the query needs.

## Change

```sql
CREATE INDEX idx_seats_avail_by_class ON seats (trip_id, class_code, status, seat_no);
```

`trip_id`/`class_code`/`status` are all equality predicates in the query
and `seat_no` is the sort column, so the index itself is already in the
exact order the query needs - and since `seat_no` is also the only
non-key column `SELECT`ed, the index alone satisfies the whole query with
no lookup into the underlying row at all (a true covering index).

Index build time on the already-populated 5,729,820-row table: **34.8s**.

## After

| id | select_type | table | type | key | key_len | rows | filtered | Extra |
|---|---|---|---|---|---|---|---|---|
| 1 | SIMPLE | seats | ref | idx_seats_avail_by_class | 389 | 648 | 100.00 | Using where; Using index |

`filtered` jumped from 3.33% to 100% - `rows` now equals the true match
count. Handler counters on the same query:

| Handler_read_key | Handler_read_next | Handler_read_rnd_next |
|---|---|---|
| 1 | 648 | 0 |

`Handler_read_next` dropped from 1224 to 648 - exactly the number of rows
actually returned, zero wasted reads. `Using index` in `Extra` confirms
MySQL never touched the clustered index (the full row) at all.

## Takeaway

The primary key `(trip_id, seat_no)` alone happened to avoid a literal
filesort (row order already matched `ORDER BY seat_no` within a trip), so
this wasn't the classic "temp table + filesort" horror story - it was the
quieter, more common cost: reading ~2x the necessary rows, and reading
full rows instead of a narrow index, on a query that runs once per class
chip rendered in the frontend. The covering index eliminates both.
