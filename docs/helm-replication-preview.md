# Durable Helm Hosted replication preview

[简体中文](helm-replication-preview.zh-CN.md) · [Documentation index](README.md)

Helm Hosted→Hosted replication copies a complete chart and optional provenance within one installation, producing a target publication with independent objects. It uses the existing default-off `GATEWAY_HELM_PREVIEW_ENABLED` flag and remains a traditional HTTP Helm preview. Apply migration `000188` before enabling it. API nodes create and read jobs; workers execute them only when `GATEWAY_WORKER_FORMATS` selects `helm` and `GATEWAY_WORKER_KINDS` selects `replication`.

## Freeze a bounded plan

Source and target must be distinct, active Helm Hosted repositories. A source reader obtains digest, size, provenanceDigest, provenanceSize and governanceVersion from `GET /api/v2/previews/helm/repositories/{id}/charts/{name}/{version}`. Pin these values unchanged. Names and versions follow the existing strict Helm name and SemVer rules. For a chart without provenance, provide an empty provenance digest and zero size; an existing provenance file cannot be dropped during replication.

A plan contains 1–100 distinct chart identities, sorted by the server into an immutable plan digest. Creation requires current source-read and target-write permission for every chart, using the same chart-name prefix grants as publishing and downloading. An independent API key may have no global role if both repository grants exist and its credential remains valid.

```http
POST /api/v2/previews/helm/replication-jobs
Authorization: Bearer <current-credential>
Idempotency-Key: release-2026-10-24
Content-Type: application/json

{
  "sourceRepositoryId": "11111111-1111-4111-8111-111111111111",
  "targetRepositoryId": "22222222-2222-4222-8222-222222222222",
  "items": [{
    "name": "service",
    "version": "1.2.3",
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "size": 12345,
    "provenanceDigest": "",
    "provenanceSize": 0,
    "sourceGovernanceVersion": "1"
  }]
}
```

Creation returns `202`, a durable job and `Location`. Replaying the same actor, authentication kind, idempotency key and plan returns the same job; changed plans or original service-account credential identity conflict. Jobs neither accept nor retain source object keys, bearer tokens or signing keys. Supported durable actors are currently configured static principals, valid persisted local/OIDC users, independent API keys and service-account credentials with a non-secret credential ID. Unpersisted OIDC subjects and derived tokens lacking revocable credential identity cannot submit durable jobs.

`GET /api/v2/previews/helm/replication-jobs/{jobId}` requires read permission on both repositories for every item. It returns the frozen plan, actor, job version/state, attempt counts, target session IDs and safe failure codes, without physical object keys or credential IDs.

## Complete closures and recovery

The worker fully reads and checks the source chart and provenance, including digests, lengths, metadata and the chart/provenance binding. Original archives and signatures are preserved. Target objects and sessions belong independently to the target repository. The worker reopens actual target bytes after uploading; corruption, truncation or missing provenance prevents publication. Checking provenance structure and its chart digest binding does not establish signer trust: consumers still use their own public keyring with `helm pull --verify`.

The target publication lock spans reservation, both uploads, readback and final commit, and is shared with normal uploads and abandoned-upload collection. The final database transaction locks current subjects/credentials, both repository grant sets and repositories, then rechecks source governance, target identity/governance, whole-closure capacity and lease. Target publication, item checkpoint and audit commit atomically. Revocation, disablement, password reset and credential expiry prevent unfinished publication. An expired worker cannot publish or mutate the replacement owner's checkpoints.

Any source tombstone, quarantine or governance-version change rejects replication, even if quarantine policy permits ordinary reads. An existing target is idempotent only when its complete bytes and governance agree; different closures, tombstones and quarantine are never overwritten or restored implicitly. Capacity includes chart and provenance, retaining references for hidden publications.

Jobs progress from `queued` to `running`, then `succeeded` or `failed`; items use `pending`, `running`, `succeeded` and `failed`. After process or database-connection reopening, a new worker can claim an expired lease, preserve completed items and fully recheck unfinished closures. Atomicity is per chart: earlier successful charts remain visible when another item fails. Recovery retries entire items, without byte-range resumption.

## Retry exact failed items

Only the original actor, authentication kind and original service credential identity can retry a failed job. Supply its current version, nonempty distinct failed-item indexes and a new retry idempotency key:

```http
POST /api/v2/previews/helm/replication-jobs/{jobId}/retry
Authorization: Bearer <original-actor-current-credential>
Idempotency-Key: retry-one
Content-Type: application/json

{"expectedVersion":"7","itemIndexes":[1,2]}
```

Success returns `202`. Exact replay does not reschedule work; changed payloads under the same key, stale versions, running jobs and completed items return `409`. Completed items retain their session, timestamp and attempt count. Plans are immutable: a changed source governance pin requires a reviewed new plan. Retry checks current authority and cannot restore revoked permissions.

## Acceptance and limits

Targeted tests cover Memory/PostgreSQL leases, concurrent claims, current subjects and both repository grants, audit rollback, complete target closures, partial success and exact retry, plus reopening after upload and after a lost commit reply. The isolated official Helm 3.19.0 scenario configures only the target and runs repo update, pull --verify and template from empty caches. It removes owned test source objects, reopens the Gateway/store connection and repeats target-only cold consumption.

This preview excludes Proxy/Group replication, federation, promotion, OCI Helm, Nexus source adapters, Console/CLI and installation presets. External dependency repositories are not traversed; dependency bytes already inside an archive are preserved unchanged. Parent roadmap #292 tracks formal admission and remaining capabilities. Isolated fixtures, CI, business migration and production deployment remain separate acceptance layers.
