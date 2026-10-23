# NuGet Hosted replication preview

[简体中文](nuget-replication-preview.zh-CN.md) · [Documentation index](README.md)

NuGet Hosted-to-Hosted replication is an explicitly enabled preview. It copies
verified packages inside one Gateway installation into independent target
publications. It does not admit NuGet as a formal format, implement a Nexus
NuGet source adapter, or implement promotion policies, Proxy/Group replication,
retention, physical GC, remote-instance federation or Console/CLI operations.
The remaining admission work stays in [the NuGet roadmap](nuget-roadmap.md).

## Enablement and roles

`GATEWAY_NUGET_PREVIEW_ENABLED=true` enables the existing preview routes and
this API. It is false by default and adds no installation presets. Only active
NuGet **Hosted** repositories with different IDs are accepted; public format
profiles and formal Console repository creation remain unchanged.

The API role creates and reads durable jobs. A worker role executes them only
when both the `nuget` worker format and `replication` worker kind are selected
by `GATEWAY_WORKER_FORMATS` / `GATEWAY_WORKER_KINDS`. Standalone nodes include
both roles. API-only nodes never execute these jobs; worker-only nodes do not
expose the management or native protocol routes. Migration `000184` must be
applied before starting the upgraded nodes.

## Freeze and submit a bounded plan

For each package, read its canonical package ID/version, SHA-256, complete size
and `governanceVersion` from the source preview package endpoint. Pin all five
values. A plan contains 1–100 distinct identities, one source repository ID and
one target repository ID. Package IDs and versions normalize according to the
native NuGet rules and the server sorts the immutable plan before hashing it.
Duplicate normalized coordinates are rejected rather than silently deduplicated.

```http
POST /api/v2/previews/nuget/replication-jobs
Authorization: Bearer <current-credential>
Idempotency-Key: release-2026-10-23
Content-Type: application/json

{
  "sourceRepositoryId": "11111111-1111-4111-8111-111111111111",
  "targetRepositoryId": "22222222-2222-4222-8222-222222222222",
  "items": [{
    "packageId": "Acme.App",
    "version": "1.0.0",
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "size": 12345,
    "sourceGovernanceVersion": "2"
  }]
}
```

Creation returns `202` and `Location` for the durable job. An exact key replay
returns the same job; a changed plan or service credential identity conflicts.
Creation and retry require current source read and target write permission for
every package ID. The package-prefix grant rules are the same as native reads
and publication. No source object key or bearer is accepted or retained in the
job. Missing repositories, cross-format/Proxy/Group targets, stale source pins,
governed sources and insufficient target capacity are rejected before queuing.

Durable execution supports the currently configured static administrator and
resolver, persisted active local/OIDC users, active standalone API keys and
active service-account credentials with their original non-secret credential
ID. It reloads current roles, account/password state and repository grants.
Standalone API keys may have no global role: explicit source-read and
target-write grants are sufficient. Missing, revoked or expired keys cannot
submit or finish a package, even if their repository grants remain.
Unpersisted external OIDC subjects and derived service-account tokens without a
revocable credential identity cannot submit a durable task. Replacing the
configured static actor does not keep the old actor's queued authority alive.

## Package commits and recovery

`GET /api/v2/previews/nuget/replication-jobs/{jobId}` requires current read
permission on both repositories for every item. It returns the fixed plan,
actor, job version/state, item attempts, target session ID, result and safe
failure code, without physical object keys, credential IDs or secrets.

Jobs move from `queued` to `running`, then `succeeded` or `failed`. Each package
moves through `pending`, `running`, `succeeded` or `failed`. A database lease and
increasing fence protect all progress and the final publication transaction.
An expired worker cannot commit, fail an item or finish a newer owner's job.
After a process or database restart, a new worker reclaims the expired lease,
keeps completed items and re-verifies unfinished packages in full. Byte offsets
are not a claim of resumable range transfer.

Every new target publication has its own session and repository-scoped object
key. The worker reads the complete source bytes, validates nupkg/nuspec identity,
SHA-256, size, dependency metadata and package hash, writes the target object,
then re-opens and verifies the actual target bytes. An acknowledged corrupt or
truncated upload remains invisible. The final database transaction locks the
current actor/credential, repository grant sets and repositories, checks the
lease, source governance, target governance and capacity, and commits the
target publication, item checkpoint and audit together. Credential expiry at
the final transaction boundary rolls the publication back.

A same-coordinate, same-content target is idempotent only after its bytes and
governance pass validation. Different bytes, tombstones, quarantine or an
incompatible target listing state produce a conflict. Replication never restores,
releases or relists an existing target. Source tombstones, any quarantine or
changed governance version reject unfinished work even when the quarantine read
policy would otherwise permit a download. Newly published targets retain the
source's current listed/unlisted state and dependency metadata; exact restore
continues to work for unlisted versions.

The atomic unit is **one package**. A failed batch can retain earlier completed
packages; it does not roll back or hide those publications. Retained jobs refer
to their original repositories and are not a repository-deletion/GC workflow.

## Retry selected failed items

Only the original durable actor, authentication kind and original service
credential identity can retry a failed job. Supply its current `version`, a
nonempty unique list of failed item indexes and a new retry idempotency key:

```http
POST /api/v2/previews/nuget/replication-jobs/{jobId}/retry
Authorization: Bearer <original-actor-current-credential>
Idempotency-Key: retry-one
Content-Type: application/json

{"expectedVersion":"7","itemIndexes":[1,2]}
```

A successful retry returns `202`. Exact key/payload replay schedules nothing
again. Reusing the key for a different version/item set, stale versions, running
jobs or completed items returns `409`. The plan and source pins cannot change.
After a source governance change, submit a separately reviewed new plan rather
than retrying a stale pin. Revoked credentials or removed grants cannot regain
authority through retry. Completed items preserve their original target session,
publication time and attempt count.

## Acceptance evidence and limits

Public HTTP/Memory tests cover normalization, idempotency, authorization,
capacity, immutable conflicts, source/target governance, partial progress and
exact retry. PostgreSQL tests cover concurrent workers, lease fences, transactional
audit rollback and actual permission/subject mutation locks. PostgreSQL/S3 tests
cover the post-upload and lost-commit-response crash windows and reopen recovery.
The native gate requires the fixed official .NET SDK `10.0.100` to restore and
execute a three-level dependency graph using only the target in distinct empty
caches, after stopping the source/worker, removing fixture source objects and
restarting Gateway, PostgreSQL and S3.

These owned local fixtures and CI are separate from production deployment,
business-source migration and release admission. The feature remains a preview;
no compatibility aliases or traffic-switch workflow are introduced.
