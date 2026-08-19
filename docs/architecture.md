# Architecture

## System overview

```mermaid
flowchart TB
    subgraph client["Client"]
        browser["Browser<br/>(embedded frontend)"]
    end

    subgraph edge["Edge"]
        gateway["gateway<br/>REST · auth · rate limit"]
    end

    subgraph core["Core services (gRPC)"]
        search["search<br/>fan-out + Redis cache"]
        catalog["catalog<br/>routes · trips · fares"]
        inventory["inventory<br/>seat/berth locking · holds · waitlist"]
        booking["booking<br/>saga orchestrator · outbox"]
        payment["payment<br/>mock gateway · ledger"]
        suppliersim["supplier-sim<br/>fakes IRCTC/Amadeus/bus GDS<br/>+ injectable chaos"]
        notification["notification<br/>Kafka consumer"]
    end

    subgraph stores["Storage"]
        mongo[("MongoDB<br/>catalog")]
        mysql[("MySQL<br/>inventory · booking · payment")]
        redis[("Redis<br/>search cache")]
        kafka[("Kafka<br/>booking-events")]
    end

    browser -->|REST| gateway
    gateway -->|gRPC| search
    gateway -->|gRPC| booking
    gateway -->|gRPC| inventory

    search -->|gRPC| catalog
    search --> redis
    catalog --> mongo

    booking -->|gRPC| inventory
    booking -->|gRPC| payment
    booking -->|gRPC| suppliersim
    booking --> mysql
    inventory --> mysql
    payment --> mysql

    booking -.->|outbox relay| kafka
    inventory -.->|outbox relay: seats freed| kafka
    kafka -.-> notification
    kafka -.->|waitlist promotion| inventory

    classDef svc fill:#1a1f36,color:#fff,stroke:#2b6bff
    classDef store fill:#2b3563,color:#fff,stroke:#6b7280
    class gateway,search,catalog,inventory,booking,payment,suppliersim,notification svc
    class mongo,mysql,redis,kafka store
```

Dotted arrows are asynchronous (Kafka); solid arrows are synchronous
(REST or gRPC). The only two dual-purpose paths are `inventory`'s outbox
(seat-freed events durably retry a promotion that already ran
synchronously - see `internal/inventory/service/service.go`'s
`tryPromoteAfterFree`) and `booking`'s outbox (confirmed/cancelled events
feed `notification`).

## The booking saga

```mermaid
sequenceDiagram
    participant U as Caller
    participant B as booking
    participant I as inventory
    participant P as payment
    participant S as supplier-sim

    U->>B: CreateBooking(trip, seats, amount)
    B->>B: insert booking (pending)
    B->>I: CreateHold(trip, seats, ttl)
    I-->>B: hold_id
    B->>B: status -> held
    B->>P: Charge(booking_id, amount)
    P-->>B: payment_id, approved
    B->>B: status -> paid
    B->>S: Confirm(booking_id, trip, seats)
    alt supplier confirms
        S-->>B: supplier_pnr
        B->>I: ConfirmHold(hold_id, booking_id)
        I-->>B: ok
        B->>B: status -> confirmed, PNR issued
        B-->>U: confirmed booking
    else supplier declines (the hard case)
        S-->>B: ErrUnavailable
        Note over B: payment already succeeded -<br/>seat can't just be released
        B->>B: status -> refunding
        B->>P: Refund(payment_id, amount)
        B->>I: ReleaseHold(hold_id)
        B->>B: status -> cancelled
        B-->>U: cancelled booking (refunded)
    end
```

Every step past the initial hold (`Charge`, supplier `Confirm`,
`ConfirmHold`, `Refund`, `ReleaseHold`, supplier `Cancel`) is idempotent
on a key derived from the booking ID. That is what makes this diagram
also describe crash recovery: the reconciliation sweeper
(`RunReconciliationSweeper`) finds any booking whose `updated_at` is
stale for its status and just re-runs the same `advance()` state machine
the synchronous path above uses - no separate "what happened while I was
down" logic. See `docs/adr/0002-resumable-saga.md`.

Train and flight follow the identical diagram from the `held` step
onward - they differ only in *how* the hold is acquired (train:
`CreateHoldByClass`, inventory picks seats or waitlists; flight:
`Hold`/`ReleaseHold` on the supplier adapter, no local inventory at all).
`advance()`, `doCharge`, `doConfirm`, and `doCompensate` do not know or
care which mode they're running.

## The three concurrency models

| Mode | Inventory owner | Locking strategy |
|---|---|---|
| Bus | We own it | Pessimistic `SELECT ... FOR UPDATE` on the seat row, local transaction |
| Train | We own it, with quotas | Row lock plus waitlist queue when the quota is exhausted |
| Flight | Provider owns it | No local lock possible. External hold (`Hold`/`ReleaseHold`), PNR reconciliation, compensating cancel |
