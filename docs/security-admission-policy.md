# Security admission policy

Security admission policies protect a Hosted repository at the point where an
immutable artifact is promoted into it. They do not change ordinary reads,
writes to the source repository, proxy resolution, or artifact intelligence
collection. Artifact quarantine is a separate source-side governance invariant:
it blocks promotion and replication even when the target admission policy is
disabled. A second, independent quarantine read policy can additionally make
protocol reads fail closed for quarantined identities.

## Policy fields

Policies are versioned per repository and use `If-Match` for optimistic
concurrency control.

| Field | Meaning |
| --- | --- |
| `enabled` | Enable admission checks for promotions into this repository. |
| `autoScanOnPublish` | Enqueue a durable asynchronous scan after each new Hosted publication. |
| `requireSignature` | Require at least one signature. |
| `requireVerifiedSignature` | Require at least one signature with `verified: true`. |
| `requireSbom` | Require at least one SBOM reference. |
| `requireProvenance` | Require build provenance metadata. |
| `requireVulnerabilityScan` | Require a vulnerability result other than `not_scanned`. |
| `maxAllowedSeverity` | Maximum accepted affected severity: `none`, `low`, `medium`, `high`, or `critical`. `unknown` is always treated as higher than `critical`. |
| `failOnScanError` | Reject a result with scanner status `error` when enabled. |
| `allowedLicenses` | Optional case-insensitive SPDX ID allowlist. Every reported license must be present. |

The default policy is disabled, allows critical findings, fails closed on scan
errors, and has no license restriction.

## Management API

Read or replace the policy at:

```http
GET /api/v2/repositories/{repositoryId}/security-policy
PUT /api/v2/repositories/{repositoryId}/security-policy
If-Match: <policy version>
```

Use the evaluation endpoint to preview a promotion without creating a job:

```http
POST /api/v2/repositories/{targetRepositoryId}/security-policy:evaluate
Content-Type: application/json

{
  "sourceRepositoryId": "<source repository UUID>",
  "coordinate": "com.example:widget:1.2.3",
  "digest": "sha256:<64 lowercase hex characters>"
}
```

The source must be active, use the same format as the target, and contain the
visible coordinate/digest identity. The caller needs administrator access to
the target and read access to the source.

## Promotion behavior

Promotion evaluates the target policy immediately before enqueueing the
promotion job. A disabled policy returns `policy_disabled` and allows the
request. An enabled policy returns `security_policy_denied` with a list of
stable reason codes when a requirement is not met. A denial is audited as
`promote.security_policy` and no lifecycle job is created.

Current reason codes are:

```text
policy_disabled
signature_required
verified_signature_required
sbom_required
provenance_required
vulnerability_scan_required
vulnerability_scan_error
license_required
license_not_allowed
low_vulnerabilities
medium_vulnerabilities
high_vulnerabilities
critical_vulnerabilities
unknown_vulnerabilities
```

## Artifact quarantine

Quarantine is versioned by immutable repository, format, coordinate, and digest
identity. It is independent of tombstones and format-native artifact state.
Read or transition the current record at:

```http
GET /api/v2/repositories/{sourceRepositoryId}/artifact-quarantine?coordinate=<coordinate>&digest=<sha256>
PUT /api/v2/repositories/{sourceRepositoryId}/artifact-quarantine?coordinate=<coordinate>&digest=<sha256>
If-Match: 0 | <current quarantine version>

{
  "state": "quarantined" | "released",
  "reason": "operator-supplied audit reason"
}
```

The initial quarantine uses `If-Match: 0`; every later transition uses the
current response `ETag`. A stale version returns `412 version_conflict`, and a
duplicate state transition returns `409 invalid_state`. Only an existing,
visible artifact may receive its first quarantine record. Release remains
possible after later lifecycle changes.

For Conan, a recipe revision and all visible package revisions below it form
one atomic distribution unit. The quarantine coordinate must therefore be the
recipe revision (`reference#recipeRevision`). Package revisions remain
independent scanner, intelligence, tombstone, and retention identities, but a
package-only quarantine is rejected because recipe promotion or replication
would otherwise carry that package to the target.

A quarantined source returns `403 artifact_quarantined` from promotion and
replication before any job or plan is created. Both worker types recheck the
same immutable identity immediately before publishing target metadata, under a
shared identity lock, so quarantining an already queued operation still keeps
the target invisible. The policy evaluation endpoint reports
`allowed: false`, `enforced: true`, and reason `artifact_quarantined` even when
the target policy itself is disabled. Release permits a later request or retry,
subject to all other admission checks.

A replication plan that encounters Quarantine in a worker is parked without
publishing metadata or consuming another retry. Release does not create a new
plan: replay the exact replication request with the same `Idempotency-Key` to
requeue and return the original plan ID.

For PyPI, one `project@version` distribution unit can contain several files.
Quarantining any visible file digest blocks promotion and replication of the
whole version. The evaluation endpoint, request-time admission, and both
worker-time checks use the same aggregate digest set. At the final publication
boundary, the worker holds the version distribution lock, re-lists the complete
visible `project@version`, and evaluates every current file digest. The original
job payload or replication checkpoint snapshot is not treated as the complete
set, so a file added and quarantined after enqueue still blocks publication.
If current PyPI membership differs from a replication plan's checkpoints but
none of the current files is quarantined, the worker parks the plan with
`replication_snapshot_changed` instead of publishing a partial version. Exact
same-key replay atomically replaces the parked plan's checkpoints with the
current complete file set and requeues the original plan ID.

Quarantine is evaluated before `Idempotency-Key` replay. If an accepted request
is replayed after its source identity becomes quarantined, the replay returns
`403 artifact_quarantined` instead of the earlier `202`. Releasing the identity
restores the original idempotent `202` response without creating a second job.
For a parked replication plan, that exact replay also requeues the original
plan rather than creating a second plan.

Current replication plans persist coordinate and digest together. Historical
plans may have both fields empty because the columns were added with backward-
compatible defaults; a new worker fails a non-terminal empty-identity plan
closed instead of publishing without a Quarantine check. Follow the drain and
worker-stop sequence in [release-readiness.md](release-readiness.md) before a
rolling upgrade so an old worker cannot bypass this invariant.

## Quarantine read enforcement

Each Hosted repository has a separate versioned read policy. It is disabled by
default so an upgrade does not change ordinary protocol reads. Administrators
can read or replace it with optimistic concurrency control:

```http
GET /api/v2/repositories/{repositoryId}/quarantine-read-policy
PUT /api/v2/repositories/{repositoryId}/quarantine-read-policy
If-Match: <policy version>

{
  "version": "<policy version>",
  "enabled": true | false
}
```

When enabled, quarantined Raw, Maven, npm, PyPI, and Conan artifacts return
`403 artifact_quarantined` for protocol `GET` and `HEAD`. OCI uses the Registry
V2 `DENIED` error code with the same stable reason. Raw listings, Maven
metadata, OCI tags, npm packuments, PyPI Simple metadata, and Conan revision
metadata omit blocked identities.

npm and PyPI enforce the whole `package@version` or `project@version`; a single
quarantined digest blocks every distribution in that version. Conan enforces
the recipe revision anchor and its package closure. OCI blocks a manifest and
the config/layer/index blobs referenced by that manifest. Group resolution
treats a higher-priority quarantined identity as policy-owned and does not fall
through to a lower-priority Hosted or Proxy member for the same identity.

Releasing the quarantine restores reads immediately. Disabling the read policy
restores the legacy behavior without changing quarantine records. Go and APT
remain outside this first read-enforcement slice because their current native
surfaces do not yet expose the same quarantine workflow.

For a gradual rollout, keep the policy disabled while scanners begin writing
artifact intelligence, use the evaluation endpoint from CI, then enable the
target repository after the expected evidence coverage is established.

The bounded external scanner transport and its multi-object input contract are
documented in [artifact-scanner-contract.md](artifact-scanner-contract.md).
Administrators or CI can enqueue a durable scan through
`POST /api/v2/repositories/{repositoryId}/artifact-scans`; the worker merges
scanner-owned fields into artifact intelligence while preserving signatures
and provenance. Hosted repositories can set `autoScanOnPublish` to schedule the
same scan automatically after a Maven, OCI, Raw, npm, PyPI, or Conan
publication becomes visible. Scheduling failures are audited but do not roll
back a successful publication.

After a promotion publishes the target artifact, the source repository's
artifact intelligence is copied by immutable identity. Existing equivalent
evidence is treated as already synchronized. If the target contains different
evidence, the gateway never overwrites it; it records a failed `intelligence`
lifecycle job for operator review. Temporary storage failures are retried by
the same follow-up job without replaying the artifact publication.

If the metadata store cannot persist either the evidence or its follow-up job,
the promotion still completes and emits the bounded
`intelligence-copy/deferred` background-operation metric. Alert on that metric
and reconcile the missing evidence after storage recovers. Repository
administrators can use `POST /api/v2/repositories/{repositoryId}/lifecycle-jobs:reconcile-intelligence`
to atomically requeue up to 100 failed or cancelled copy jobs without
republishing the artifact.
