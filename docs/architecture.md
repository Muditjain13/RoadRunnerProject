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

    subgraph core[" "]
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

    classDef client fill:#0ea5e9,color:#fff,stroke:#0369a1,stroke-width:2px
    classDef edge fill:#8b5cf6,color:#fff,stroke:#5b21b6,stroke-width:2px
    classDef read fill:#14b8a6,color:#fff,stroke:#0f766e,stroke-width:2px
    classDef write fill:#f97316,color:#fff,stroke:#c2410c,stroke-width:2px
    classDef money fill:#ef4444,color:#fff,stroke:#b91c1c,stroke-width:2px
    classDef ext fill:#eab308,color:#1f2937,stroke:#a16207,stroke-width:2px
    classDef async fill:#ec4899,color:#fff,stroke:#9d174d,stroke-width:2px
    classDef sql fill:#2563eb,color:#fff,stroke:#1e3a8a,stroke-width:2px
    classDef doc fill:#16a34a,color:#fff,stroke:#14532d,stroke-width:2px
    classDef cache fill:#dc2626,color:#fff,stroke:#7f1d1d,stroke-width:2px
    classDef bus fill:#475569,color:#fff,stroke:#0f172a,stroke-width:2px

    class browser client
    class gateway edge
    class search,catalog read
    class inventory,booking write
    class payment money
    class suppliersim ext
    class notification async
    class mysql sql
    class mongo doc
    class redis cache
    class kafka bus

    style client fill:#0ea5e91a,stroke:#0ea5e9,stroke-dasharray:4 3
    style edge fill:#8b5cf61a,stroke:#8b5cf6,stroke-dasharray:4 3
    style core fill:#64748b14,stroke:#64748b,stroke-dasharray:4 3
    style stores fill:#2563eb14,stroke:#2563eb,stroke-dasharray:4 3

    linkStyle 13,14,15,16 stroke:#ec4899,stroke-width:2px
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
