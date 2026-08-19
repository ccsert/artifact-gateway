# Go Hosted Uses One Canonical Module ZIP Publication

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
standard. The first admitted Go Hosted profile declares only `read`, `publish`,
and `browse`. Delete/restore, retention, reclaim, promotion, replication,
quarantine-read enforcement, and checksum-database mirroring remain separate
capabilities and must not be advertised until executable.
