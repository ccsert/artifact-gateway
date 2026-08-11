# Artifact scanner contract

The `internal/scanning` module is the controlled seam between Artifact Gateway
and an external security scanner. It does not execute user-configured commands
and does not allow a scanner to mutate repository state directly.

The adapter is enabled at process startup with `GATEWAY_SCANNER_ENDPOINT`.
Administrators can enqueue a scan for an immutable artifact; a `scan` worker
resolves protocol-owned assets, streams them to the adapter, and optimistically
merges scanner-owned fields into stored artifact intelligence. Hosted
repositories can also opt in to asynchronous scan scheduling after each new
publication.

## Configuration

Set `GATEWAY_SCANNER_ENDPOINT` to enable the adapter. The other settings are
optional:

| Variable | Default | Meaning |
| --- | --- | --- |
| `GATEWAY_SCANNER_NAME` | `artifact-scanner` | Bounded scanner identity recorded with vulnerability summaries. |
| `GATEWAY_SCANNER_TOKEN` | empty | Bearer token sent to the configured endpoint. |
| `GATEWAY_SCANNER_TIMEOUT` | `2m` | Per-scan request deadline, between `1s` and `30m`. |
| `GATEWAY_SCANNER_HEALTH_ENDPOINT` | empty | Optional read-only scanner health and vulnerability database metadata endpoint. |
| `GATEWAY_SCANNER_HEALTH_TIMEOUT` | `2s` | Health request deadline, between `1s` and `30s`. |
| `GATEWAY_SCANNER_DATABASE_MAX_AGE` | `24h` | Maximum accepted vulnerability database age, between `1m` and `720h`. |
| `GATEWAY_SCANNER_MAX_RESPONSE_BYTES` | `524288` | Maximum JSON response, between 1 KiB and 8 MiB. |
| `GATEWAY_SCANNER_MAX_ARTIFACT_BYTES` | `21474836480` | Maximum streamed logical artifact, up to 1 TiB. |
| `GATEWAY_SCANNER_FORMATS` | all scanner-supported formats | Comma-separated format allowlist. |

Both endpoints must be HTTPS outside localhost/loopback and cannot contain
credentials, query parameters, or fragments. Scanner settings without a scan
endpoint are rejected so a misspelled deployment does not silently look
enabled. Omitting only the health endpoint keeps scanning available but marks
scanner health and database freshness as unknown. Startup logs and diagnostics
expose only sanitized status, identity, versions, timestamps, and formats; the
endpoints and token remain private.

## Durable execution

Queue a scan through the management API with a caller-owned idempotency key:

```http
POST /api/v2/repositories/{repositoryId}/artifact-scans
Authorization: Bearer <token with repositories:intelligence>
Idempotency-Key: ci-build-1842-scan
Content-Type: application/json

{"coordinate":"com.example:widget:1.2.3","digest":"sha256:<64 lowercase hex characters>"}
```

The API returns `202` with the lifecycle job. Repeating the same key and body
returns the existing job; reusing the key for another identity returns `409`.
The repository format must be present in `GATEWAY_SCANNER_FORMATS`.
Capability discovery reports this manual-scan path as `artifactScanning` and
reports publication-hook availability separately as `publicationScanning`, so
Proxy caches and formats without a native publish hook never expose a
non-functional automatic-scan control.

Set `autoScanOnPublish: true` through the repository security policy to enqueue
the same durable scan automatically after a new Maven, OCI, Raw, npm, PyPI, or
Conan Hosted publication becomes visible. The setting only affects future
publications. Scheduling is best effort and does not roll back a successful
publication when the lifecycle queue or policy store is unavailable. Each
repository, format, coordinate, and digest identity receives a stable
`publish-scan:` idempotency key, so protocol retries do not create duplicate
scan jobs. Enqueue successes and failures are recorded in the audit log.

Worker nodes must include `worker` in `GATEWAY_NODE_ROLES`, allow `scan` in
`GATEWAY_WORKER_KINDS`, and use the same scanner configuration. Jobs use the
existing PostgreSQL lease, retry, cancellation, and run-now controls. A report
replaces only scanner-owned SBOM, license, and vulnerability fields. Existing
signatures and provenance remain unchanged, including when a concurrent writer
updates them while the scan is running.

Format resolvers verify the queued coordinate and digest against repository
metadata before opening object bytes. Maven SNAPSHOT resolution selects the
timestamped build identified by coordinate and digest. OCI resolution walks
the selected manifest or index and its referenced config/layers. npm, Raw, and
PyPI resolve immutable files; Go resolves cached info/mod/zip assets; Conan
resolves recipe or package revision assets.

All native Hosted layouts are supported. npm, PyPI, and Go Proxy repositories
use the same native metadata and can scan artifacts whose byte objects are
already cached. Maven, OCI, Raw, and Conan Proxy repositories still use their
legacy cache indexes and are rejected at enqueue time until dedicated resolver
adapters are available. Scans never fetch mutable upstream content.

Artifact intelligence currently uses coordinate and digest as its immutable
identity. If two Maven SNAPSHOT publishes have the same primary digest, they
share one intelligence identity; callers cannot select between those builds'
different ancillary files until the identity model includes the build number.

## Logical artifact input

A scan targets one immutable identity:

- repository ID;
- format;
- coordinate;
- SHA-256 artifact digest;
- one or more named assets, each with a byte size, SHA-256 digest, optional
  media type, and a streaming opener.

This shape is deliberate. Raw and npm commonly provide one object, while OCI,
Maven, Conan, and PyPI may require several objects to represent the logical
artifact a scanner must inspect. Object keys and object-store credentials are
never sent as metadata.

Before accepting a result, the HTTP adapter streams every asset through a
SHA-256 verifier and rejects truncated, oversized, or changed content. The
default limits are 256 assets, 20 GiB across the logical artifact, a two-minute
request timeout, and a 512 KiB response. Adapter construction may lower or
raise the byte limits within hard safety bounds.

## HTTP adapter

The adapter sends `POST` with `multipart/form-data` and
`X-Artifact-Scanner-Schema: v1`. The first part is named `metadata`, uses
`application/json`, and has this shape:

```json
{
  "schemaVersion": "v1",
  "repositoryId": "repository UUID",
  "format": "maven",
  "coordinate": "com.example:widget:1.2.3",
  "digest": "sha256:<64 lowercase hex characters>",
  "assets": [
    {
      "part": "asset-0",
      "path": "widget-1.2.3.jar",
      "digest": "sha256:<64 lowercase hex characters>",
      "size": 1234,
      "mediaType": "application/java-archive"
    }
  ]
}
```

The following parts use the names declared by `assets[].part`. The adapter
streams them without buffering the complete artifact in process memory.

The scanner returns `application/json`:

```json
{
  "schemaVersion": "v1",
  "sboms": [
    {
      "mediaType": "application/spdx+json",
      "digest": "sha256:<64 lowercase hex characters>",
      "url": "https://scanner.example.test/sboms/123",
      "size": 456
    }
  ],
  "licenses": [
    {"spdxId": "Apache-2.0", "name": "Apache License 2.0", "source": "manifest"}
  ],
  "vulnerability": {
    "status": "affected",
    "critical": 0,
    "high": 1,
    "medium": 0,
    "low": 0,
    "unknown": 0,
    "findings": [
      {
        "id": "CVE-2026-1234",
        "source": "nvd",
        "severity": "high",
        "component": "pkg:maven/com.example/widget@1.2.3",
        "version": "1.2.3",
        "fixedVersion": "1.2.4",
        "location": "widget-1.2.3.jar",
        "title": "Example remote code execution",
        "description": "A crafted payload can execute code.",
        "url": "https://scanner.example.test/vulnerabilities/CVE-2026-1234",
        "cvssScore": 8.1,
        "cvssVector": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
      }
    ]
  }
}
```

Unknown response fields, invalid URLs or digests, negative counts, oversized
collections, non-JSON responses, and trailing JSON values are rejected. The
Gateway records the configured adapter name and its own completion time rather
than trusting scanner-supplied identity or timestamps.

`findings` is optional so existing summary-only `v1` adapters remain valid. If
it is present, it is the complete set represented by the severity counters:
every finding must use `critical`, `high`, `medium`, `low`, or `unknown`, and
the counters must match exactly. Duplicate ID/source/component/version/location
identities are rejected. A report contains at most 1,000 findings and remains
subject to the configured response-byte limit. An `affected` report must
contain at least one counted vulnerability; `clean` and `not_scanned` reports
cannot contain non-zero counts. This invariant is enforced both at the scanner
boundary and the management intelligence write boundary.

## Health and vulnerability database freshness

When `GATEWAY_SCANNER_HEALTH_ENDPOINT` is set, the Gateway sends a bounded
`GET` request with the same Bearer credential, `Accept: application/json`, and
`X-Artifact-Scanner-Schema: v1`. The endpoint returns:

```json
{
  "schemaVersion": "v1",
  "status": "healthy",
  "version": "0.61.0",
  "database": {
    "version": "2026-08-10",
    "updatedAt": "2026-08-10T06:00:00Z"
  }
}
```

`status` is `healthy`, `degraded`, or `unhealthy`. Scanner and database
versions are optional; when `database` is present, `updatedAt` is required.
The response is limited to 64 KiB, rejects unknown fields and timestamps more
than five minutes in the future, and follows the same no-redirect and no-error-
body policy as scanning.

The Gateway assigns `checkedAt` locally and compares `database.updatedAt` with
`GATEWAY_SCANNER_DATABASE_MAX_AGE`. A healthy scanner with an older database is
reported as degraded. `GET /api/v2/diagnostics` exposes this sanitized status,
the configured format coverage, scanner/database versions, and freshness to
administrators; the Console presents it inside the existing dependency panel.
Scanner health is operational evidence rather than a process readiness gate,
so a temporary scanner outage does not take repository reads and writes down.

## Transport policy

- HTTPS is mandatory outside loopback development endpoints.
- Credentials may be sent only as a Bearer header; endpoint user info, query
  strings, fragments, and credential-bearing redirects are rejected.
- Redirects are never followed because they could replay credentials or
  artifact bytes to a different endpoint.
- Scanner error bodies are not propagated into lifecycle state or operator
  responses.
- Reports can contain SBOM references, licenses, vulnerability summaries, and
  bounded per-vulnerability findings. They cannot replace publisher signatures
  or provenance.

Malicious-component quarantine and repository-read blocking remain future
work.

## Durable status and reconciliation

Every queued scan is a `scan` lifecycle job. Its payload stores the immutable
`format`, `coordinate`, and `sha256` digest, so the lifecycle job is the
authoritative status rather than a second status table. Administrators can
query `GET /api/v2/repositories/{repositoryId}/artifact-scans` for one identity;
`never` means no job exists yet. The same identity can be manually queued with
`POST` and a new idempotency key after a previous job is terminal; active jobs
are returned instead of duplicated. The Console polls pending, running, and
retrying jobs.

`POST /api/v2/repositories/{repositoryId}/artifact-scans:reconcile` enumerates
visible immutable publications and compares them with the newest scan job. It
uses the stable `publish-scan:<sha256>` key for missing jobs, retries failed or
cancelled jobs, and leaves active or completed jobs unchanged. Actionable
identities are ordered ahead of active and completed scans, so repeated bounded
calls progress through repositories larger than one batch. Candidate coordinates
include Maven builds, OCI manifests, Raw paths, npm/PyPI versions, and both Conan
recipe and package revisions. Reconciliation does not require automatic
publication scanning to be enabled; this permits explicit historical backfills.
The operation is bounded by a caller-supplied limit and is protected by the
repository `repositories:intelligence` permission.
