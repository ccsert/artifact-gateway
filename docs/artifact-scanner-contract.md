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

### Bundled Trivy reference adapter

The optional Compose `scanner` profile builds `Dockerfile.scanner` and runs the
bundled `reference-scanner` HTTP adapter with Trivy. The Trivy runtime is pinned
to the official `aquasec/trivy:0.72.0` multi-architecture manifest digest. The
adapter is a non-root sidecar with no Linux capabilities. It shares the Gateway
network namespace, listens only on `127.0.0.1:18082`, and is not published on a
host or Docker-network port. This preserves the loopback-only HTTP development
exception without weakening the HTTPS rule for external scanners.

To enable it, copy these values into `.env` and run `make dev` (or the normal
Compose `up` command):

```dotenv
COMPOSE_PROFILES=scanner
GATEWAY_SCANNER_ENDPOINT=http://127.0.0.1:18082/v1/scan
GATEWAY_SCANNER_HEALTH_ENDPOINT=http://127.0.0.1:18082/v1/health
GATEWAY_SCANNER_NAME=trivy-reference
GATEWAY_SCANNER_FORMATS=maven,raw,npm,pypi,go,conan
GATEWAY_SCANNER_TOKEN=replace-with-a-local-scanner-token
GATEWAY_SCANNER_TIMEOUT=10m
GATEWAY_SCANNER_MAX_ARTIFACT_BYTES=1073741824
```

The sidecar accepts no command flags from a request. It materializes each asset
under a private temporary root after rechecking its declared size and SHA-256,
runs fixed `trivy filesystem` vulnerability/license analysis, and converts the
same native report to CycloneDX. The CycloneDX document is content-addressed,
persisted in the capacity-bounded `gateway-scanner-sboms` volume, and available
to Gateway-side consumers from the Bearer-protected internal SBOM URL recorded
by the Gateway. The adapter does not expose SBOM bytes on a host port. The
Gateway also receives bounded license evidence and a
vulnerability summary plus up to 200 detailed findings. A result that would
exceed the Gateway's default response bound automatically falls back to the
complete severity summary. Trivy's database cache is kept in the
`gateway-trivy-cache` volume so routine restarts do not redownload it. Database
downloads use the configured `GATEWAY_EGRESS_PROXY` when present. The 10-minute
Gateway timeout shown above also covers the first database download; later
scans normally reuse the cache.

License evidence is aggregated by SPDX ID rather than component source, so a
frequently repeated allowed license cannot hide a later disallowed license.
More than 100 unique license IDs fails the scan explicitly instead of returning
an incomplete set that could make admission policy appear satisfied.

This filesystem reference adapter intentionally supports Maven, Raw, npm,
PyPI, Go, and Conan assets. It rejects OCI input because a set of manifest and
layer blobs is not equivalent to an OCI root filesystem with layer and whiteout
semantics applied. Gateway's scanner contract and OCI resolver remain available
for a format-aware external OCI scanner; the reference adapter does not claim
results it cannot derive correctly.

Reference-adapter settings are deployment-only and are not returned to the
Console:

| Variable | Default | Meaning |
| --- | --- | --- |
| `REFERENCE_SCANNER_LISTEN_ADDRESS` | `127.0.0.1:18082` | Loopback listener; non-loopback addresses are rejected. |
| `REFERENCE_SCANNER_TOKEN` | empty | Optional Bearer token; Compose maps `GATEWAY_SCANNER_TOKEN`. |
| `REFERENCE_SCANNER_TRIVY_BINARY` | `trivy` | Operator-controlled Trivy executable path. |
| `REFERENCE_SCANNER_SCAN_TIMEOUT` | `10m` | Fixed-command scan deadline, between `1s` and `30m`. |
| `REFERENCE_SCANNER_HEALTH_TIMEOUT` | `10s` | Version/database probe deadline, between `1s` and `30s`. |
| `REFERENCE_SCANNER_MAX_ARTIFACT_BYTES` | `1073741824` in Compose | Multipart artifact bound; the local profile also uses a bounded temporary filesystem. |
| `REFERENCE_SCANNER_MAX_OUTPUT_BYTES` | `67108864` | Per native Trivy/CycloneDX file bound, up to 512 MiB. |
| `REFERENCE_SCANNER_MAX_CONCURRENT` | `1` | Process-wide scan concurrency, between 1 and 32. |
| `REFERENCE_SCANNER_SBOM_DIR` | `/var/lib/reference-scanner/sboms` | Private content-addressed CycloneDX store. |
| `REFERENCE_SCANNER_BASE_URL` | `http://127.0.0.1:18082` | URL prefix recorded for protected SBOM retrieval. |
| `REFERENCE_SCANNER_MAX_SBOM_BYTES` | `2147483648` | Total SBOM-store capacity; a full store fails new scans instead of breaking existing references. |

The Compose profile additionally limits the sidecar to one scan at a time, 256
processes, two CPUs, 4 GiB of memory, and a 1.5 GiB temporary filesystem. The
image root is read-only. Raising artifact size or concurrency requires an
explicit matching review of those container limits.

With the base stack already running, `make reference-scanner-smoke` starts the
actual Compose-profile sidecar, proves its UID, read-only root, CPU/memory/PID,
tmpfs, volume, and shared-network constraints, performs a real Trivy filesystem
and CycloneDX conversion, and checks container health. It removes only the
scanner sidecar afterward. Run it when changing the adapter, Trivy pin, image,
or Compose profile.

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

The adapter sends `POST` with `multipart/form-data`,
`X-Artifact-Scanner-Schema: v1`, and
`X-Artifact-Scanner-Accept-Schema: v2, v1`. The request body remains `v1`
because detailed findings only extend the response. The first part is named
`metadata`, uses `application/json`, and has this shape:

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

The scanner returns `application/json`; this example uses the negotiated `v2`
report:

```json
{
  "schemaVersion": "v2",
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

An upgraded scanner returns `v2` only when the accept header advertises it. A
scanner receiving a request without that header must keep returning a strict
summary-only `v1` document, so an older Gateway does not reject `findings` as
an unknown field during a rolling upgrade. The Gateway accepts either `v1` or
`v2`; a `v1` response containing `findings` is invalid.

`findings` is optional in `v2`. If it is present, it is the complete set
represented by the severity counters:
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

Administrators can place the scanned immutable distribution anchor into the
independent, versioned quarantine workflow, which blocks Promotion and
Replication without changing scanner-owned evidence. For Conan, the recipe
revision is the atomic promotion/replication unit and the only valid quarantine
anchor. Package revision scans and Artifact Intelligence remain independently
addressable, but a package revision cannot be quarantined on its own. Automatic
scanner-triggered quarantine and repository-read blocking remain future work.

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

The Console exposes these operations on the repository detail **Scanning** tab.
It reports manual-scan and scan-on-publication availability separately, accepts
one protocol-owned canonical coordinate and SHA-256 digest for an explicit scan,
lists recent scan lifecycle jobs, and provides the bounded historical
reconciliation action when the repository has a publication-scan resolver. The
default picker reads `GET /api/v2/repositories/{repositoryId}/artifact-identities`
with `purpose=scan`; it includes historical versions and both Conan recipe and
package revisions instead of reconstructing identities from browse results.
Exact manual entry remains an advanced recovery path. When capability discovery
reports no scanner, the tab stays visible but disables mutation and points the
operator to `GATEWAY_SCANNER_ENDPOINT`, `GATEWAY_SCANNER_FORMATS`, and Worker
`scan`-kind deployment configuration. Scanner endpoints and tokens remain
deployment secrets and are never returned to the browser.

Operators can use the bundled Trivy reference adapter or provide another
contract-compatible HTTPS scanner. Scan scheduling remains asynchronous and
best effort, so a scan or enqueue failure never rolls back a successful
publication. Results do not automatically quarantine an artifact or change
repository-read behavior. Governed runtime scanner configuration and finding-
driven automatic quarantine remain separate follow-up slices.
