# ADR 0002: Resumable saga via idempotent steps, not a saga log

## Status
Accepted

## Context
The booking saga (search -> hold -> payment -> supplier confirm -> ticket
issued) can fail or be interrupted at any step: a gateway timeout, a
process crash, a network partition to one of inventory/payment/supplier-sim.
This project's hardest requirement is the case where payment has already
succeeded but a later step fails - money has moved, so the seat cannot
simply be dropped and forgotten. Something has to reliably finish the job
(either complete the remaining steps or compensate) even if the original
request's process is long gone.

Two ways to build that "something":

1. **Event-sourced saga log.** Record every step attempt/outcome as an
   event; a saga log processor replays events to determine what to do
   next. Gives a full audit trail, but adds an entire subsystem (the log,
   its processor, replay logic) whose only job is answering "what state is
   this saga actually in."
2. **Resumable steps via idempotency.** Make every step past the first
   safe to call again with the same key, and let a dumb reconciliation
   sweeper just re-run the saga's own forward-progression logic
   (`advance()`) on any booking that hasn't moved in too long.

## Decision
Use option 2. Every step after the initial hold - `Charge`, the supplier's
`Confirm`, inventory's `ConfirmHold`, `Refund`, `ReleaseHold`, the
supplier's `Cancel` - is idempotent on a key derived from the booking ID.
This is enforced and tested independently in each owning service
(inventory, payment, suppliersim), not assumed by booking.

The reconciliation sweeper (`internal/booking/service/sweeper.go`) does
not contain any "what might have happened" recovery logic. It finds
bookings stuck in a non-terminal status past a staleness threshold and
calls the exact same `advance()` the synchronous request path calls.
Every external call `advance()` might repeat is safe to repeat, so
resuming a booking that already completed some steps just re-derives
ground truth (a Charge replay returns the existing payment; a Confirm
replay returns the existing PNR) and continues from there.

The one exception is the very first `CreateHold` call, which is NOT
idempotent-by-key - see the package doc on `internal/booking/service` and
`docs/adr/0001-hold-expiry-sweeper.md`. That gap is closed by a different,
already-existing mechanism (the hold's TTL), not by adding idempotency
here too.

## Rationale
- No new subsystem. The saga log approach effectively requires its own
  mini event-sourcing engine; the idempotent-steps approach reuses
  transactional writes and locks the codebase already has.
- The sweeper is trivially testable: seed a booking in a given status,
  call `advance()`, assert the end state. No event replay logic to get
  wrong.
- Idempotency-by-key is a non-negotiable design principle for this repo
  ("idempotency everywhere - every payment call, every supplier
  confirm") - this design gets saga resumability "for free" from a
  property the system needs regardless.

## Consequences
- Every new saga step this pattern is extended to (e.g. train's quota
  reservation in Phase 3) must be built idempotent-by-key from day one, or
  it breaks resumability, not just that one step's own correctness.
- There is no saga-level audit log beyond `bookings.status` and
  `bookings.failure_reason` plus whatever `outbox_events` captures at
  terminal transitions. If a full step-by-step audit trail becomes a real
  requirement (e.g. for dispute resolution), that is a genuine reason to
  revisit this decision - it is not free to add later, since it means
  introducing exactly the event log this ADR chose to avoid.
- The sweeper's staleness threshold is a tradeoff between "resume stuck
  bookings quickly" and "don't re-attempt a step that's still legitimately
  in flight." The chaos testing tool (`make chaos`) is a good way to
  tune this empirically once the full saga is running end-to-end.
