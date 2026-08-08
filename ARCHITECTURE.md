# Artifact Gateway Architecture

Artifact Gateway is a repository manager for OCI, Maven, Raw, and Conan 2. It
separates durable metadata, immutable artifact bytes, protocol handling, and
background lifecycle work so the same binary can run as a compact single node
or as role-specific cluster nodes.

## System context

```text
native clients and Console
           |
           v
   API nodes / standalone
      |             |
      v             v
 PostgreSQL      S3 / MinIO
      ^             ^
      |             |
 scheduler ----> worker pools
```

PostgreSQL is the source of truth for repositories, authorization, artifact
metadata, lifecycle state, audit records, idempotency, and background leases.
S3/MinIO stores verified object bytes addressed by digest. Gateway processes do
not keep durable artifact state on local disk.

## Runtime roles

One image supports four runtime configurations through `GATEWAY_NODE_ROLES`:

| Role | Responsibility | Exposed surface |
| --- | --- | --- |
| `api` | Native protocols, public browse, management API | Full HTTP surface |
| `scheduler` | Discover and enqueue periodic lifecycle work | Operations only |
| `worker` | Lease and execute durable background work | Operations only |
| `standalone` | API, scheduler, and worker in one process | Full HTTP surface |

Workers can be constrained by artifact format and job kind. Nodes coordinate
through PostgreSQL leases, fencing tokens, idempotency keys, advisory locks,
`FOR UPDATE SKIP LOCKED`, and best-effort `LISTEN/NOTIFY` wakeups. Notifications
are never the source of truth; polling recovers from missed notifications.

Deployment constraints and topology examples live in
`docs/distributed-deployment.md`.

## Major code boundaries

| Path | Ownership |
| --- | --- |
| `cmd/gateway` | Process composition, runtime roles, lifecycle start/stop |
| `internal/app` | HTTP composition and application use cases |
| `internal/authorization` | Authentication and authorization policy |
| `internal/protocol` | Native OCI, Maven, Raw, and Conan behavior |
| `internal/repository` | Domain records plus memory/PostgreSQL persistence |
| `internal/objectstore` | Content-addressed object storage |
| `internal/lifecycle` | Durable job state and shared lifecycle semantics |
| `internal/replication` | Checkpointed cross-repository replication |
| `internal/maintenance` | Retention, reclaim, cache, and deletion work |
| `internal/admin/openapi` | Generated management API server contract |
| `api/openapi` | Editable API contract sources and bundled artifacts |
| `console` | React and Ant Design administrative/public Console |

Protocol packages own wire compatibility and format-specific coordinates.
Application handlers authorize requests and orchestrate domain interfaces;
they must not recreate protocol rules. Repository implementations own
transactional persistence but not HTTP response behavior.

## Repository model

- **Hosted** repositories accept native publication and own artifact lifecycle.
- **Proxy** repositories read approved upstreams and apply cache policy.
- **Group** repositories resolve ordered Hosted and Proxy members behind one
  client endpoint.

Publication verifies bytes before metadata becomes visible. Deletion is
logical and asynchronous: protocol access stops in `deleting`, workers advance
the repository to `deleted`, and metadata remains as an audit anchor. Artifact
tombstones support format-aware restoration where the lifecycle contract
allows it.

Promotion records an immutable source snapshot and reuses verified
content-addressed bytes. It does not rename a snapshot version into a release
version. Replication owns durable byte transfer and records checkpoints and
integrity verification separately.

The normative lifecycle decisions are recorded in `docs/adr/` and
`docs/artifact-lifecycle-contract.md`.

## API contract ownership

`api/openapi/native-hosted.yaml` and its sibling YAML files are the editable
contract. Bundled JSON, the Console client under `console/src/client`, and the
Go server contract under `internal/admin/openapi` are generated together.

All management API evolution must pass `make openapi-check` and the API
compatibility check. Generated files are committed so reviewers can see the
exact client and server impact of a contract change.

## Consistency and failure model

- PostgreSQL transactions define metadata visibility and state transitions.
- Object bytes may exist before publication commits; reclaim work removes
  unreferenced bytes only after a grace period and a reference recheck.
- Background work is at-least-once. Lease tokens fence stale workers and
  handlers must be idempotent.
- S3/MinIO and PostgreSQL are shared cluster dependencies; a worker cannot rely
  on another node's memory or filesystem.
- Readiness checks dependencies required by the configured role, while
  liveness only reports process health.

## Change rules

1. Add behavior through the owning domain or protocol boundary, not directly
   in an unrelated HTTP handler.
2. Make schema changes with forward-only migrations.
3. Preserve immutable published coordinates and content digests.
4. Keep retries idempotent and fence writes from expired workers.
5. Evolve external APIs through OpenAPI first.
6. Prove database locking and object-store behavior with integration tests.
