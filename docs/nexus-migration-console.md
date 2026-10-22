# Nexus migration in the Console

[简体中文](nexus-migration-console.zh-CN.md) | [Documentation index](README.md)

Platform administrators open **Operations → Migration sources**. The
**Connection and mapping** view manages the [saved source](nexus-source-discovery.md).
**Batches and acceptance** reads and controls [durable batches](nexus-migration-batches.md).
Deep links use `/operations?tab=migrations&view=batches&batch=<batch UUID>`.
They contain identifiers only. Members cannot open the route or call its APIs.

## Apply an explicit plan

Run source preflight and preview the selected mappings. The Hosted action carries
the source and target-name intent to the batch form. Alternatively create a batch
directly or choose **Rescan and catch up** from an existing batch. Every start
re-reads the saved source and requires preflight for that connection version.

Select at most ten supported Maven release, Raw, OCI or Conan 2 Hosted sources and bind each to
a real, active Hosted target of the same format. A previewed name is resolved
only when it already exists in the current repository catalog; it is never
treated as an invented repository UUID. Create missing targets explicitly in
repository management. Applying the mapping persists these target IDs as
immutable batch inputs; it does not inherit source policies, credentials, Group
membership or Proxy configuration.

The runtime capabilities endpoint supplies the actual object target and physical
pool IDs. Capacity is operator-reported evidence, not a Gateway measurement or
reservation. All six byte values start empty and must be known nonnegative exact
integers; an explicit zero is allowed. Supply ISO observation/expiry times with a
timezone, no future observation, and a window of at most one hour. Unknown,
expired or mismatched evidence blocks execution. Resume/retry starts with empty
fields and requires an observation strictly newer than the batch's last one.
The download peak must cover at least the largest complete source-asset spool.
Never substitute log, temporary-directory or unrelated host disk space.

## Observe and control

Scanning metadata, sealed plan entries, unique source total, extra scan-stage
source verification, copy-stage source reads, unique target read-back bytes and
visible identities are separate facts. A missing total remains unknown. Extra
source verification fills missing manifest hashes; copy bytes include retries
and revalidation of existing identities. Neither is target visibility. There is
no inferred percentage or blanket “caught up” claim. Exceptions retain their
own conflict, failure, unsupported, missing-source and source-deletion counts.

Closing the browser or switching views stops observation, while the worker
continues. Active detail and entry queries poll independently: byte/checkpoint
updates do not necessarily change a batch's CAS version. Returning to a visible
page or refreshing re-reads server facts. Pause drains in-flight work. Resume
and retry require fresh evidence; retry is batch-level and requeues only eligible
failed/source-missing entries below the server's attempt limit. Cancel keeps
committed content and does not delete source or target data. An unsealed failed
scan instead offers a fresh-evidence retry that fully rescans the same batch.
A new catch-up batch
performs a complete enumeration and target revalidation; source deletions are
reported, never mirrored destructively.

A source request refused with 401/403 hides loaded source metadata, preflight
and mapping observations, and closes editor, deletion and batch-draft forms.
Changing sources, switching views or remounting the panel does not restore the
cache. Only a successful fresh read started after the refusal restores access;
preflight and capacity must then be entered again. Temporary service errors
(500) retain the last authorized observation with a refresh-failure warning.
Changing the signed-in token or actor starts a new read scope automatically;
the previous identity's late responses cannot restore or refuse the new scope.

Controls use the captured `If-Match` version and a stable `Idempotency-Key` for
identical retries within the open form. Version conflicts require reading the
current batch. A network/server failure can leave the outcome unknown; first
read the server, then retry the same input if appropriate. Successful command
responses are followed by GET because a replay body can contain an older
checkpoint. Opening, closing, refreshing or polling never issues control POSTs.

## Read and export an acceptance observation

Entry pages expose source and target paths, exact sizes, complete SHA-256 hashes,
attempts and target verification flags. Load all pages before treating the
inventory as complete. Export re-reads the batch, loaded entry pages and target
details; it records separate batch/entry observation times and marks an unfinished
scan, unconsumed cursor or incomplete inventory as incomplete. It projects bounded
server facts and never exports source credentials, arbitrary upstream errors or
download validators.

The report shows current native target endpoints, format-specific client configuration
examples, unsupported/conflict counts and residual dependencies. Its **target-only
cold-client evidence** and **final catch-up after source freeze** stay **unverified**
because the batch API contains no such acceptance evidence. Backend `completed`
and the last server update do not establish either fact. Official client fixture
gates elsewhere also cannot verify an operator's particular batch.

Before cutover, explicitly freeze source writers, run the final full rescan,
reconcile exceptions and test real clients with an empty cache against only the
target. Check Proxy/Group dependencies, unsupported formats/SNAPSHOT, permissions,
publisher credentials and CI/client endpoints. If the target receives new writes,
rollback needs reconciliation and backfill of those writes before switching
back. The Console does not change CI, DNS or Ingress, stop Nexus, or authorize
production traffic switching.

## Recurring migration plans

Open **Recurring migration plans** or `/operations?tab=migrations&view=schedules`.
Create a plan from a fully successful completed batch and set an interval of
1–43,200 minutes. The plan freezes that batch's source version, target mappings
and native policy. Editing any of these bindings requires a new successful
batch and a new plan. The additive schema is
`000166_nexus_migration_schedules.sql`; administrator APIs live under
`/api/v2/migrations/nexus/schedules`.

The existing scheduler role dispatches ordinary durable batches; the existing
migration worker performs each complete rescan, copy and verification. Closing
the browser has no effect. Due times are persisted in UTC. Startup dispatches
one overdue occurrence and coalesces missed intervals without backlog. The
next eligible time is dispatch time plus the interval. An active batch prevents
another occurrence, including a manual start against its targets.

Each occurrence consumes one fresh operator-reported capacity observation.
**Supply capacity observation** starts with empty fields. Its observation time
must be strictly newer than both the previous supplied and consumed observation;
the target/pool binding and existing one-hour freshness limit still apply.
The product has no trusted remote object-pool observer: missing or expired
observations enter `waiting_capacity`, retaining the due time and bounded reason.
The system never extends old evidence or substitutes local disk measurements.
This delivery is therefore not fully unattended migration.

**Pause plan** and **Stop plan** prevent future dispatches while retaining the
active batch and published content. Stop is permanent; pause can be resumed
with fresh evidence. Manage a paused or blocked active batch through its own
batch page before resuming the plan. Source/policy drift and real unsupported
payloads block dispatch. Unexpected dispatch errors retry at one-minute
intervals at most three times, then require explicit recovery. No plan operation
deletes source or target content.

The next eligible time and last successful sync are read from persisted state.
A submitted batch is not success. Last success advances only after a complete,
verified terminal batch within the plan-supported native scope. Original
unsupported counts and details remain unchanged. The only reporting exceptions
are the exact existing Maven regeneration reasons for canonical metadata paths
or checksum sidecars whose release asset is present in the verified batch;
SNAPSHOT, unknown payloads, missing POM/required assets and metadata lookalikes
such as `maven-metadata.xml.evil` remain blockers. This timestamp does not imply
that the entire source is cleared or that cold-client cutover is verified.

## Verification boundaries

Focused state tests cover exact capacity/freshness, source-version mismatches,
real target binding, CAS/idempotency, same-version checkpoint refresh, pagination,
safe export and retained/denied reads. The real-route layout gate exercises both
themes at 1440px/390px with bounded 16/24px rhythm, long hashes and paths, and
administrator deep-link denial. Linux visual baselines retain the existing
desktop Ant Design layout.

`console/e2e/nexus-batches-live.spec.ts` is an opt-in owned fixture gate. Set
`MIGRATION_BATCH_FIXTURE_CONFIG` to its isolated runtime descriptor, run a Console
proxy against that Gateway, and use `PLAYWRIGHT_EXTERNAL_SERVER=1` plus the actual
`PLAYWRIGHT_PORT`. The descriptor identifies its synthetic source, targets,
approved TLS/egress policy and independently controlled worker. The gate refuses
an unowned project, explicitly measures its test RustFS `/data` pool for test
evidence, and never supplies such measurements for product users. It exercises
real API/PG/object bytes and worker behavior; fixture observations are not real
Nexus inventory, remote CI, production cutover or an operator's cold-client proof.
