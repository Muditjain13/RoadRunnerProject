# Deferred secondary indexes during bulk seat load

The seed spec calls for this explicitly: "Disable secondary index builds
during the bulk load, then add indexes afterward. Record the timing
difference."

## Method

Same seed run both times: `make seed` full profile (90 days forward, 1
trip/day, 90 routes, bus + train) into an empty `seats` table, batched
multi-row `INSERT ... ON DUPLICATE KEY UPDATE` at 1000 rows/batch
(`internal/inventory/repo/seed.go`). Both runs produced the same
**5,729,820 rows** (the seat generation is deterministic - same RNG seed,
same trip/class/capacity data either way).

Run 1 loaded with `seats`' three secondary indexes present throughout
(`idx_seats_hold_id`, `idx_seats_booking_id`, and the covering index from
[seat-availability-index.md](seat-availability-index.md)). Run 2 dropped
all three first, loaded into a bare-primary-key table, then created them
afterward. Both runs were otherwise identical - same machine, same
already-warm MySQL instance, run back to back with no other write load in
between.

## Results

| | Load time | Index build time | Total |
|---|---|---|---|
| Indexes present throughout | 342.7s | - (built incrementally during load) | **342.7s** |
| Indexes deferred | 244.9s | 92.3s | **337.2s** |

Deferring was faster to load in isolation (244.9s vs 342.7s, ~29% less
time for the insert phase itself) but the gap almost entirely reappears
once the deferred indexes are built afterward - net difference across the
whole operation was under 2%.

## Why the effect is smaller than the general rule of thumb suggests

Two of the three secondary indexes here - `hold_id` and `booking_id` -
are on columns that are `NULL` for every freshly-seeded seat (a seat only
gets a `hold_id` or `booking_id` once a real hold/booking touches it).
B-tree maintenance for a run of identical `NULL` keys is cheap relative
to the classic case this optimization targets: a index on high-cardinality
data being built row-by-row interleaved with unrelated writes. The one
index with real cardinality here, the covering index from
seat-availability-index.md, is also the most expensive of the three to
build standalone (its 34.8s dominates the 92.3s rebuild total) - which
is exactly what you'd expect, and matches the covering-index page's own
finding that this index is doing real, non-trivial work.

## Takeaway

The optimization is real and measurable (single run - not repeated
trials, so treat the ~2% net figure as indicative rather than precise),
but its size depends on which indexes are actually being deferred. It
pays off proportionally more the more of the deferred indexes are on
genuinely selective columns being populated with real data during the
load - not the case for two of the three indexes on this particular
table at seed time, since a freshly-seeded seat has no hold or booking
yet by definition.
