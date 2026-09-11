# Go Hosted Uses One Canonical Module ZIP Publication

[简体中文](0004-go-hosted-publication.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

The official `GOPROXY` protocol defines immutable reads but no upload
operation. Artifact Gateway therefore keeps every read under the standard
`/go/<repository>/<escaped-module>/@v/...` layout and defines one explicit
Gateway publication extension for Hosted Repositories:

```text
PUT /go/<repository>/<escaped-module>/@v/<escaped-version>.zip
```

The request body is a canonical Go module ZIP. The Gateway validates its
module path, semantic version, archive layout, size limits, and top-level
`go.mod` with `golang.org/x/mod`. The requested module and version must match
both the ZIP root and the `module` directive. The Gateway derives the `.mod`
representation from that file and generates the `.info` representation with
the first publication time; clients never upload either derived value.

Publication locks the `module@version` coordinate, stores the verified
content-addressed `.info`, `.mod`, and `.zip` objects, and makes all three
representations visible in one PostgreSQL transaction. A first publication
returns `201`. Replaying the same module ZIP is idempotent and returns `200`
without changing the first publication time. Reusing the coordinate with
different ZIP or `go.mod` bytes returns `409`. A failed or conflicting request
never exposes a partial version.

Before writing any previously missing object, the Gateway persists an internal
reclaim intent. The reclaim worker serializes on the same object lock, retains
objects referenced by a committed publication, and retries deletion of
unreferenced objects after database or object-store failures. This is crash
recovery for the publication transaction boundary, not the user-facing
delete/restore/reclaim lifecycle capability described below.

The extension requires authenticated Repository write permission and is
available only for Go Hosted Repositories. Go Proxy Repositories remain
read-only and keep verified read-through caching. Groups combine Hosted and
Proxy members with Hosted-first conflict resolution and the standard
`GOPROXY` read surface.

This decision satisfies the separate-contract requirement in
`0003-protocol-only-formats.md`; it does not present `PUT` as a Go ecosystem
standard. The admitted Go Hosted profile now declares the independently gated
`read`, `publish`, `browse`, `delete`, `restore`, `retain`, `reclaim`, `promote`,
and `replicate` operations. Promotion and replication preserve the complete
`.info`/`.mod`/`.zip` snapshot and publish only to Hosted targets.
Checksum-database mirroring remains a separate capability and must not be
advertised until executable.

Update (2026-09-11): quarantine-read enforcement, originally listed above as a
separate pending capability, is now delivered for Go Hosted and Go Group. A
default-disabled per-Hosted read policy hides a quarantined `module@version`
from `@v/list` and `/@latest`, returns `403` for every `info`/`mod`/`zip`
representation, and prevents Group fallback. See
[security-admission-policy.md](../security-admission-policy.md).

Update (2026-09-11): authenticated upstream Proxy credentials are delivered for
Go Proxy Repositories. A Go Proxy Repository may store a `none`, `basic`, or
`bearer` upstream credential; the secret is sealed with
`GATEWAY_SETTINGS_ENCRYPTION_KEY` under the `repository-upstream-auth` purpose
and is never returned by the management API. `basic` and `bearer` are the only
admitted schemes, switching to `none` removes the stored credential, the
credential is applied only to the Go Proxy upstream fetch, and it is stripped on
cross-host redirects. Every other format and Repository type rejects
`upstreamAuth` with an explicit `400` rather than ignoring it silently.
