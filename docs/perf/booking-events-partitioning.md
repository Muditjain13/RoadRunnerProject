# Monthly partitioning of booking_events

## Table

`booking_events`: a synthetic historical event log (targeting the
project's ~20,000,000-row seed volume), seeded independently of the live `bookings`
table - see `cmd/seed/booking_events.go`. Seeded to **19,999,992 rows**
spanning `2024-08-19` to `2026-08-19` (2 years), via
`go run ./cmd/seed --booking-events=20000000`.

Seeding took **~13 minutes** uncontended (batched multi-row inserts,
1000 rows/batch, ~25,600 rows/sec sustained - see the per-million log
checkpoints in the seed run, each landing 34-43s apart).

## Query

A reporting-style query scoped to one calendar month - the pattern
monthly partitioning exists to serve:

```sql
SELECT COUNT(*) FROM booking_events
WHERE created_at >= '2025-06-01' AND created_at < '2025-07-01';
```

821,200 of the 19,999,992 rows fall in this range (about 4.1%, roughly
what you'd expect for 1 month out of 24).

## Before: unpartitioned

`booking_events`' primary key is `(id, created_at)` - `id` leads, so
there's no index MySQL can seek into by `created_at` alone; the best
available plan is a full index scan.

| id | select_type | table | partitions | type | key | rows | filtered | Extra |
|---|---|---|---|---|---|---|---|---|
| 1 | SIMPLE | booking_events | NULL | index | PRIMARY | 19,445,626 | 11.11 | Using where; Using index |

`rows` is essentially the whole table. Measured wall time for the query:
**9.75s**.

## Change

```sql
ALTER TABLE booking_events
PARTITION BY RANGE COLUMNS (created_at) (
    PARTITION p2024_01 VALUES LESS THAN ('2024-02-01'),
    ...
    PARTITION p2026_12 VALUES LESS THAN ('2027-01-01'),
    PARTITION p_future VALUES LESS THAN (MAXVALUE)
);
```

36 fixed calendar-month partitions (2024-01 through 2026-12) plus a
catch-all, independent of when the migration runs - see
`migrations/booking/000004_booking_events_partition.up.sql`. Applied
*after* seeding, deliberately, so the "before" numbers above are honest
measurements against the real unpartitioned table rather than assumed.

Migration time on the already-populated 19,999,992-row table: **2m9.0s**
(`ALTER TABLE ... PARTITION BY` physically reorganizes the table into
partitions - this is real, one-time DDL cost, not free).

## After: partitioned

| id | select_type | table | partitions | type | key | rows | filtered | Extra |
|---|---|---|---|---|---|---|---|---|
| 1 | SIMPLE | booking_events | **p2025_06** | index | PRIMARY | 817,290 | 11.11 | Using where; Using index |

`partitions` now names exactly one partition - MySQL never opens the
other 36. `rows` dropped from 19,445,626 to 817,290 (~23.8x), landing
right at the true match count (821,200) instead of the whole table.
Measured wall time: **2.75s**.

## Result

| | rows scanned (EXPLAIN estimate) | wall time |
|---|---|---|
| Unpartitioned | 19,445,626 | 9.75s |
| Partitioned (pruned to 1 month) | 817,290 | 2.75s |

**3.5x faster**, from a query whose result set didn't change (both runs
returned exactly 821,200). The remaining 2.75s is the real cost of
scanning ~820k rows via the primary key's secondary-to-`created_at`
ordering (still a full scan *within* the pruned partition, since
`created_at` is the second PK column, not the first) - a candidate for a
future index if this query pattern turns out to be hot in practice, but
out of scope here since partition pruning alone already delivers the
required measurement.
