# Architecture diagrams

[简体中文](architecture-diagrams.zh-CN.md) · [Architecture](../ARCHITECTURE.md) · [PostgreSQL capabilities](postgresql-capabilities.en.md)

The generated visual overviews make the main component and deployment choices
easy to scan. The executable Mermaid diagrams below remain the precise,
reviewable source of truth for architecture relationships.

## Visual system overview

![Artifact Gateway lightweight system architecture](assets/artifact-gateway-system-architecture.png)

## System and storage boundaries

```mermaid
flowchart LR
    subgraph clients[Clients]
        Console[Web Console]
        Native[OCI, Maven, npm, PyPI, Go, APT, Conan, Raw clients]
    end

    Gateway[Artifact Gateway<br/>native protocols and management API]
    PG[(PostgreSQL 16<br/>metadata, authorization, audit,<br/>jobs, leases, locks)]
    S3[(S3-compatible storage<br/>immutable verified bytes)]
    Upstream[Allowlisted upstream registries]
    Scanner[Optional scanner / signer adapters]

    Console --> Gateway
    Native --> Gateway
    Gateway -->|transactions and coordination| PG
    Gateway -->|content-addressed objects| S3
    Gateway -->|Proxy reads| Upstream
    Gateway -->|bounded adapter calls| Scanner
```

PostgreSQL is the only database and coordination service. S3-compatible object
storage is a separate byte plane; the local stack provides it with RustFS.

## Compact standalone and split-role deployment

![Artifact Gateway standalone and distributed deployment topology](assets/artifact-gateway-deployment-topology.png)

```mermaid
flowchart TB
    subgraph standalone[Compact deployment]
        One[One Gateway process]
        OneAPI[API]
        OneScheduler[Scheduler]
        OneWorker[Worker]
        One --- OneAPI
        One --- OneScheduler
        One --- OneWorker
    end

    subgraph distributed[Split deployment using the same image]
        APIs[API nodes × N]
        Scheduler[Scheduler × 1 normally]
        Workers[Workers × N<br/>filtered by format and job kind]
    end

    PG[(Shared PostgreSQL)]
    S3[(Shared S3-compatible storage)]

    OneAPI --> PG
    OneScheduler --> PG
    OneWorker --> PG
    OneAPI --> S3
    OneWorker --> S3

    APIs --> PG
    Scheduler --> PG
    Workers --> PG
    APIs --> S3
    Workers --> S3
```

The standalone process is the default. Splitting roles changes placement, not
the database, queue, or object-storage contracts; no separate message broker or
service-discovery system is introduced.

## Publication and visibility boundary

```mermaid
flowchart LR
    Upload[Native client upload] --> Stage[Stage bytes in object storage<br/>not protocol-visible]
    Stage --> Verify[Verify digest, archive identity,<br/>and format contract]
    Verify --> Lock[Lock coordinate and quota<br/>inside PostgreSQL transaction]
    Lock --> Publish[Commit references and<br/>visible metadata atomically]
    Publish --> Read[Protocol and Console reads]

    Stage -. transaction or validation failure .-> Intent[Unreferenced object intent]
    Intent --> Recheck[Grace period and reference recheck]
    Recheck --> Reclaim[Reclaim bytes from object storage]
```

Object bytes may exist before publication, but metadata is exposed only after
verification and the PostgreSQL transaction commit. Reclaim never treats a
failed transaction as proof that bytes are safe to delete; it rechecks durable
references after a grace period.

## Durable background work

```mermaid
sequenceDiagram
    participant A as API / Scheduler
    participant P as PostgreSQL
    participant W1 as Worker A
    participant W2 as Worker B
    participant O as Object storage

    A->>P: INSERT job ON CONFLICT DO NOTHING
    P-->>W1: LISTEN/NOTIFY wake hint
    P-->>W2: LISTEN/NOTIFY wake hint
    par concurrent claims
        W1->>P: FOR UPDATE SKIP LOCKED
        W2->>P: FOR UPDATE SKIP LOCKED
    end
    P-->>W1: job 1 + lease token
    P-->>W2: job 2 + lease token
    W1->>O: verify / copy / reclaim
    W2->>O: verify / copy / reclaim
    W1->>P: fenced checkpoint and terminal state
    W2->>P: fenced checkpoint and terminal state
    Note over W1,W2: Polling recovers work when notifications are lost
```

`LISTEN/NOTIFY` reduces latency but is never the queue of record. Durable rows,
`SKIP LOCKED`, lease expiry, and fencing tokens provide recovery after a worker
or connection disappears.
