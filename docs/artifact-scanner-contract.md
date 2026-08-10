# Artifact scanner contract

The `internal/scanning` module is the controlled seam between Artifact Gateway
and an external security scanner. It does not execute user-configured commands
and does not allow a scanner to mutate repository state directly.

This is currently a transport and validation foundation. The adapter can be
enabled at process startup with `GATEWAY_SCANNER_ENDPOINT`; automatic job
creation, format-specific asset resolution, and merging a successful report
into stored artifact intelligence are separate runtime work and are not yet
enabled by configuration.

## Configuration

Set `GATEWAY_SCANNER_ENDPOINT` to enable the adapter. The other settings are
optional:

| Variable | Default | Meaning |
| --- | --- | --- |
| `GATEWAY_SCANNER_NAME` | `artifact-scanner` | Bounded scanner identity recorded with vulnerability summaries. |
| `GATEWAY_SCANNER_TOKEN` | empty | Bearer token sent to the configured endpoint. |
| `GATEWAY_SCANNER_TIMEOUT` | `2m` | Per-scan request deadline, between `1s` and `30m`. |
| `GATEWAY_SCANNER_MAX_RESPONSE_BYTES` | `524288` | Maximum JSON response, between 1 KiB and 8 MiB. |
| `GATEWAY_SCANNER_MAX_ARTIFACT_BYTES` | `21474836480` | Maximum streamed logical artifact, up to 1 TiB. |
| `GATEWAY_SCANNER_FORMATS` | all lifecycle formats | Comma-separated format allowlist. |

The endpoint must be HTTPS outside localhost/loopback and cannot contain
credentials, query parameters, or fragments. Scanner settings without an
endpoint are rejected so a misspelled deployment does not silently look
enabled. Startup logs and preflight diagnostics expose only enabled state,
name, and format count; the endpoint and token remain private.

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
    "medium": 2,
    "low": 3,
    "unknown": 0
  }
}
```

Unknown response fields, invalid URLs or digests, negative counts, oversized
collections, non-JSON responses, and trailing JSON values are rejected. The
Gateway records the configured adapter name and its own completion time rather
than trusting scanner-supplied identity or timestamps.

## Transport policy

- HTTPS is mandatory outside loopback development endpoints.
- Credentials may be sent only as a Bearer header; endpoint user info, query
  strings, fragments, and credential-bearing redirects are rejected.
- Redirects are never followed because they could replay credentials or
  artifact bytes to a different endpoint.
- Scanner error bodies are not propagated into lifecycle state or operator
  responses.
- Reports can contain SBOM references, licenses, and vulnerability summaries.
  They cannot replace publisher signatures or provenance.

The next runtime layer must preserve these properties while adding durable
scan jobs, format-owned asset resolvers, optimistic intelligence merging,
audit records, metrics, and administrator retry controls.
