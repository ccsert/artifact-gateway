# Distributed Deployment

[简体中文](distributed-deployment.zh-CN.md) · [Documentation index](README.md)

Artifact Gateway stores metadata and durable work in PostgreSQL and Asset bytes
in shared S3-compatible object storage. Gateway processes keep no artifact state
that another instance must read locally, so the service can scale horizontally.

## Node Roles

The same image selects responsibilities with `GATEWAY_NODE_ROLES`:

| Role | Responsibility | External routes |
| --- | --- | --- |
| `api` | Protocol handling, management API, public search | Full Gateway API |
| `scheduler` | Periodic scans and durable retention/reclaim/cache/audit task creation | Operational endpoints only |
| `worker` | Claims and executes durable work, replication, and promotion | Operational endpoints only |
| `standalone` | Enables all three roles | Full Gateway API |

An unset value means `standalone`. Do not combine `standalone` with another role.

Workers can narrow their format and job responsibilities:

```env
GATEWAY_NODE_ROLES=worker
GATEWAY_WORKER_FORMATS=oci
GATEWAY_WORKER_KINDS=reclaim,replication
```

`GATEWAY_WORKER_FORMATS` accepts `maven`, `oci`, `raw`, `conan`, `npm`, `pypi`,
and `apt`. APT currently runs only reclaim for management-preview uploads; this
does not make Hosted APT public. Job kinds include `promotion`, `replication`,
`retention`, `reclaim`, `intelligence`, `deletion`, `scan`, `recovery`, `cache`,
`audit`, and `webhook`.

`intelligence` copies delayed artifact intelligence after promotion. `scan`
starts only when the node has `GATEWAY_SCANNER_ENDPOINT`; its independent
`GATEWAY_SCANNER_FORMATS` may include Proxy-only Go and allows scanner isolation.
`webhook` claims global deliveries from the PostgreSQL durable outbox and ignores
format filters. Without filters, a worker handles every applicable format and job.

Filters constrain Workers only; the Scheduler discovers work for every format.
Cache reclaim is claimed separately for OCI, Raw, and Conan. Maven cache expiry
is evaluated during reads and has no independent reclaim job.

## Deployment Constraints

1. Every node uses the same PostgreSQL database and S3 bucket. The bundled
   baseline uses RustFS.
2. A separate migration job finishes before application replicas start.
   Gateway processes do not race to run migrations.
3. Give each instance a stable `GATEWAY_INSTANCE_ID` for logs and diagnosis.
   Every process start also receives a unique session ID, so duplicate instance
   IDs cannot overwrite sessions.
4. Budget primary, cache coordinator, artifact lock, and notification pools per
   replica. The default maximum is `32 + 8 + 4 + 2 = 46` connections per
   instance. Size the three database pool variables against PostgreSQL
   `max_connections`, leaving room for migrations and administration.
5. `LISTEN/NOTIFY` is only a wake hint. Durable tables and leases are authoritative,
   and polling recovers work after lost notifications.

## Recommended Topology

```text
               +-------------------+
clients ------>| api x 2            |
               +---------+---------+
                         |
                 PostgreSQL + S3
                         |
       +-----------------+------------------+
       |                                    |
  scheduler x 1                     workers x N
  all formats                       format-specific
```

API nodes scale behind a load balancer. One Scheduler is normally sufficient.
With multiple replicas, `scheduled_tasks` uses `FOR UPDATE SKIP LOCKED`, advances
`next_run_at` during the claim, and creates downstream idempotency keys from the
schedule and delivery identity. Recovery enqueues one missed delivery rather
than replaying every missed interval. Workers scale by format; claims combine
`FOR UPDATE SKIP LOCKED`, advisory locks, and lease tokens.

Each process writes instance ID, startup session, roles, Worker filters, and
heartbeat to `runtime_node_sessions`, while maintaining the `runtime_nodes`
compatibility projection. Graceful shutdown marks the session `offline`.
Abnormal exits become `stale` after 30 seconds without a heartbeat and `offline`
after two minutes. Offline records are retained for seven days by default:

```env
GATEWAY_RUNTIME_NODE_RETENTION=168h
GATEWAY_RUNTIME_NODE_PRUNE_INTERVAL=1h
```

`GET /api/v2/runtime/nodes` returns the node inventory and cluster-health
summary, including missing API/Scheduler/Worker roles, duplicate instance IDs,
and stale heartbeats. This is operational visibility only; task leases remain
the claim authority.

`GET /api/v2/diagnostics` reports build, runtime roles, dependency availability,
node health, and repository queues inside the administrator boundary. It returns
fixed states and sanitized detail, never database/S3 endpoints, environment
variables, tokens, or credentials. Operators can copy it from Console system diagnostics.

## Standalone Fallback

Local development and small installations continue to use:

```env
GATEWAY_NODE_ROLES=standalone
```

This mode requires no separate queue or service discovery and preserves the
original memory and deployment footprint.
