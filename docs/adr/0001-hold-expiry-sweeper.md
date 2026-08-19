# ADR 0001: Hold expiry via a polling sweeper, not Kafka delayed messages

## Status
Accepted

## Context
A seat hold has an 8-minute TTL. Something has to notice when a hold
expires and release its seats back to `available`, or a seat held by an
abandoned checkout is unbookable forever.

Two options:

1. A goroutine on a ticker in the inventory service, polling
   `holds WHERE status = 'active' AND expires_at < now()`.
2. Publish a delayed Kafka message at hold-creation time (e.g. via a delay
   topic + retry/backoff consumer, or a scheduled-message plugin) and let
   a consumer release the hold when the message is finally delivered.

## Decision
Use the polling sweeper (option 1).

## Rationale
- Kafka has no native delayed-delivery primitive. Every workaround (delay
  topics, external schedulers, consumer-side sleep-and-retry) adds a
  moving part whose only job is to reinvent a cron tick.
- The correctness of a hold's expiry does not depend on sub-second
  timeliness - a seat sitting `held` for a few extra seconds past its TTL
  is a non-issue. A short poll interval (e.g. 5-10s) is indistinguishable
  from an event-driven release in practice.
- A ticker-driven sweeper is trivial to reason about, trivial to test
  (advance a fake clock, assert the query and the release), and trivial to
  operate (no dead-letter topic, no delay-topic partition to monitor).
- The sweeper polls the same MySQL instance that owns the holds table
  anyway - no cross-system consistency concern between "the message fired"
  and "the DB row is actually still active".

## Consequences
- Sweep interval is a tunable tradeoff between DB polling load and how
  close to the TTL a hold actually gets released; start conservative
  (e.g. every 5-10s) and tighten only if idle-hold seat churn matters.
- If hold volume ever grows large enough that full-table polling on
  `(status, expires_at)` becomes expensive, the existing index on that
  pair is what to check first - see `internal/inventory/repo` migrations.
- This does not generalize to every "do X later" need in the system. It is
  specific to hold expiry, where late is harmless. Do not reach for this
  pattern for anything where a missed or delayed action is a correctness
  problem.
