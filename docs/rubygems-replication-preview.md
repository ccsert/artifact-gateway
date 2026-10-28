# Durable RubyGems Hosted replication preview

[简体中文](rubygems-replication-preview.zh-CN.md) · [Documentation index](README.md)

RubyGems Hosted→Hosted replication copies original `.gem` archives within one
installation into independent target objects. It shares the default-disabled
`GATEWAY_RUBYGEMS_PREVIEW_ENABLED` flag. Apply the append-only migrations,
including `000190_native_rubygems_replication.sql`, before enabling it. API nodes
create/read jobs; only worker nodes with `rubygems` in `GATEWAY_WORKER_FORMATS`
and `replication` in `GATEWAY_WORKER_KINDS` execute them. No formal format profile,
Console/agctl command or installation preset is added.

## Pin exact server identities

Source and target must be distinct active RubyGems Hosted repositories. Obtain
each source identity with package read authority:

```text
GET /api/v2/previews/rubygems/repositories/{sourceId}/packages/{name}/{version}/{platform}
```

The response contains `name`, the archive's original `version`, canonical
`platform`, complete SHA-256 `digest`, `size` and `sourceGovernanceVersion`.
Use these server-owned values unchanged. Names preserve case; versions use
RubyGems canonical equivalence for conflict detection while preserving archive
spelling. `ruby` and `java` are separate variants. Duplicate canonical
name/version/platform identities are rejected. A yanked or quarantined source
is unavailable here regardless of its quarantine read policy.

```text
POST /api/v2/previews/rubygems/replication-jobs
Authorization: Bearer <current credential>
Idempotency-Key: <stable request key>
Content-Type: application/json

{
  "sourceRepositoryId": "<source UUID>",
  "targetRepositoryId": "<target UUID>",
  "items": [{
    "name": "owned_top",
    "version": "1.2.0",
    "platform": "ruby",
    "digest": "sha256:<64 lowercase hex characters>",
    "size": 4096,
    "sourceGovernanceVersion": "1"
  }]
}
```

Plans accept 1–100 exact variants, each at most 64 MiB. List every dependency
variant explicitly; the worker does not traverse dependency sources. The
202 receipt has a stable job ID, canonical plan digest, version, state and item
checkpoints. An exact actor/key replay returns the same job; a changed plan or
original credential identity conflicts. Job receipts never expose physical
object keys, credential/session IDs, secrets or lease tokens.

`GET /api/v2/previews/rubygems/replication-jobs/{jobId}` requires current read
permission for every package in both repositories. Creation/execution require
source read and target write permission for every item. The original user's
session/version/expiry, API key or service account credential is revalidated
under the final publication transaction's locks; expired or revoked original
credentials cannot be refreshed by replaying a job. Submit a new job with a new
key after changing the credential identity.

## Publication and recovery

The worker verifies complete source bytes and archive metadata, holds the same
target publication lock as native push, reserves capacity, writes an independent
target object and fully reads it back. Target visibility, publication audit and
successful item checkpoint commit together, fenced by current lease owner,
token, generation and database time. Waiting for a lock or audit cannot let an
expired owner publish. A restarted worker reclaims expired leases; completed
items survive and are never rolled back with a failed batch.

Any source governance-version change invalidates its original pin, including
yank followed by restore or quarantine followed by release. An existing target
is idempotent only when full original bytes and metadata agree and it remains
neither yanked nor quarantined. Replication never restores or releases a target,
rewrites a conflicting archive, or replaces corrupt existing bytes. Hidden
identities retain their capacity and reservation protection.

A failed job supports selective recovery by its original actor with current
authority and its current job version:

```text
POST /api/v2/previews/rubygems/replication-jobs/{jobId}/retry
Idempotency-Key: <stable retry key>
Content-Type: application/json

{"expectedVersion":"<current job version>","itemIndexes":[1]}
```

Only explicitly selected failed items are queued. Successful items and source
pins stay fixed. An exact retry/key replay schedules no extra attempt. Safe
item codes distinguish `access_denied`, `source_changed`, `target_conflict`,
`identity_conflict`, `quota_exceeded` and `transfer_failed`; raw storage errors
are not returned in receipts.

## Verification and limits

`make native-rubygems-e2e` runs the shared Memory/PostgreSQL contract, isolated
PostgreSQL/RustFS transactions and the pinned official Ruby 3.4.4, RubyGems 3.6.7
and Bundler 2.6.7 clients. The production Gateway persists an API-only job,
restarts before worker execution and performs cold target-only transitive
`gem install`, compact-index and `bundle install --full-index` checks. Complete
downloads must match original archive hashes and dependency metadata, including
after process restart. The gate records resource ownership and cleanup.

This slice excludes Proxy/Group, promotion, federation, formal admission,
scanning/GC, backup admission, Nexus migration and production deployment.
[Parent #293](https://github.com/ccsert/artifact-gateway/issues/293) remains open;
[implementation #472](https://github.com/ccsert/artifact-gateway/issues/472) covers
only this preview. Local fixtures, CI, business migration and production remain
separate acceptance layers. Native client configuration is described in the
[Hosted preview guide](rubygems-hosted-preview.md).
