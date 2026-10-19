# PyPI file yanking

[简体中文](pypi-yank.zh-CN.md) · [Protocol matrix](protocol-compatibility.md)

Yanking is advisory file-selection metadata defined by the [PyPA file-yanking
specification](https://packaging.python.org/en/latest/specifications/file-yanking/).
It does not delete or rewrite a wheel or source distribution. A range requirement
normally skips yanked files; an exact `==` or `===` pin can still download one,
and pip reports its reason. Quarantine and tombstones enforce separate policies.

## Manage a Hosted version

In the Console, open a Hosted PyPI project and select its version. The file table
shows each file's state and reason. A caller with `repositories:write` can open
**Manage yanking**, choose the whole current version or an exact filename, and
yank or restore it. A platform administrator role is not required when the
repository grant permits the operation. Proxy and Group pages remain read-only.

The generated management contract exposes:

```text
GET   /api/v2/repositories/{repositoryId}/pypi/projects/{project}/versions/{version}/yank
PATCH /api/v2/repositories/{repositoryId}/pypi/projects/{project}/versions/{version}/yank
```

GET returns `repositoryId`, normalized `project`, `version`, an opaque `revision`,
and all visible files with `filename`, `yanked`, `reason`, `digest`, and `size`.
Its strong `ETag` is the quoted revision. The complete visible file membership
participates in that revision: a concurrent upload, restore, yank or unyank
invalidates an old selection. Read access follows the repository's existing
read and explicit anonymous-read policy. PATCH requires an authenticated writer.

Send one current `If-Match` and a JSON body:

```json
{"filename":"widget-1.0-py3-none-any.whl","yanked":true,"reason":"ABI regression"}
```

`yanked` is required. Omit `filename` to update all currently visible files in the
selected version atomically. Future uploads are not automatically yanked; refresh
and explicitly include them in another version operation. A reason is optional,
valid UTF-8, and limited to 1024 bytes without control characters. Restoring
(`yanked:false`) clears the reason. Empty reasons are valid. Unknown fields,
null field values and malformed or oversized input fail with `400`.

An outdated, weak, wildcard or multi-value validator fails with `412` before a
mutation. Refresh the state and confirm the new selection. Repeating an already
current state is successful without advancing its revision. The change and its
`pypi.yank` / `pypi.unyank` success audit commit together; a failed audit rolls the
metadata back. Audit evidence includes the file or version scope, previous and
committed revisions, reason, and changed-file count. Archive size and SHA-256
remain unchanged. The Hosted PyPI capability catalog advertises `yank`; the
capability does not bypass authorization or synthesize a missing store port.

## Native reads, Proxy and lifecycle

HTML Simple links carry `data-yanked="reason"` for yanked files, including an
empty attribute when there is no reason. JSON Simple files carry `yanked:false`,
`true` for an empty reason, or the reason string. A mirrored JSON empty string is
also treated as yanked. Reasons are escaped as text, never rendered as HTML.

Proxy metadata revalidation preserves these attributes and the existing verified
archive cache. Invalid or conflicting upstream metadata cannot silently unyank
a cached file; the existing stale-data path reports its warning. Group keeps
the selected first member's complete file metadata and bytes, including its
yank declaration; a lower-priority copy cannot erase it. No client credential
is forwarded to the upstream. Authenticated upstream indexes remain unsupported.

Tombstone/restore retains yanking. Promotion and replication copy the source's
current declaration when publishing a new target file. A retry finding an
already-published identical target keeps that target's local governance state.
Neither operation rewrites an existing target's advisory metadata. Backup and
restore retain the PostgreSQL columns alongside the verified immutable objects.
Migration `000152_pypi_yank.sql` is forward-only; older restored schemas must run
the normal forward migrations before serving traffic.

## Evidence and limits

`make native-pypi-e2e` pins pip **26.2** and twine **6.2.0** in a private virtual
environment. It checks Hosted, Proxy and Group selection with fresh client
destinations and no client cache: wheel/sdist range skips, exact-pin reason
warnings, mixed file states, unyank/revalidation and stopped-upstream reads.
Every selected archive is compared in full, by size and SHA-256, with its fixture.

`scripts/pypi-yank-storage-e2e.sh` creates an owned Compose project on dynamic
ports and removes it afterward. It tests PostgreSQL CAS between Gateway
instances, transactional audit failure, durable grants, RustFS restart,
promotion/replication and tombstone/restore. An actual `pg_dump` is restored into
an empty database, and verified archive bytes are read through a fresh S3 bucket.
This is isolated acceptance evidence, not a production restore or deployment.
It adds no Warehouse JSON API, vulnerability database or private upstream auth.
The command map records future agctl yank commands as planned.
