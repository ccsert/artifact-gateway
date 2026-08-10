# Security admission policy

Security admission policies protect a Hosted repository at the point where an
immutable artifact is promoted into it. They do not change ordinary reads,
writes to the source repository, proxy resolution, or artifact intelligence
collection.

## Policy fields

Policies are versioned per repository and use `If-Match` for optimistic
concurrency control.

| Field | Meaning |
| --- | --- |
| `enabled` | Enable admission checks for promotions into this repository. |
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

For a gradual rollout, keep the policy disabled while scanners begin writing
artifact intelligence, use the evaluation endpoint from CI, then enable the
target repository after the expected evidence coverage is established.

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
