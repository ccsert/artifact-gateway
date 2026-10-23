# Nexus PyPI Hosted migration

[简体中文](nexus-pypi-migration.zh-CN.md) · [Migration batches](nexus-migration-batches.md)

Issue #461 adds PyPI Hosted byte imports to the durable Nexus batch worker.
The target must be an existing, active Gateway PyPI Hosted repository. A batch
maps an explicitly selected Nexus Hosted repository to that target; discovery
and planning do not create repositories or copy source permissions.

## Admitted source shape

The source reader uses bounded, authenticated, read-only Nexus Components REST
pages and their Assets records, plus the same repository's HTML Simple project
pages. Components supply project/version identity; filenames and archive core
metadata must agree with it. Projects use Python's normalized name. The first
slice accepts canonical release versions, wheel files and `.tar.gz` source
distributions. Other formats, ambiguous identities and unsupported archive
shapes fail explicitly.

A project Simple page must enumerate the same complete file set as Components,
with a SHA-256 fragment on every file link. Links must resolve inside the
configured HTTPS origin and repository, without credentials, queries or encoded
path ambiguity. Duplicate filenames, source paths and HTML attributes are
rejected. Each file retains its `Requires-Python`, yank flag and yank reason;
`Requires-Python` must agree with archive core metadata. HTML is converted into
a sorted private snapshot, so irrelevant HTML formatting is not an identity.
The snapshot is never exposed as a native artifact or a public batch asset.

The per-file compressed limit is 64 MiB, expanded archive limit 256 MiB,
per-project Simple/snapshot limit 2 MiB, metadata member limit 2 MiB, maximum
1,000 project files and 10,000 archive members. The existing 64 MiB manifest
metadata budget includes copies of a project snapshot in separate version
entries. Archives are parsed without extraction or execution. Unsafe paths,
links, duplicate members, multiple metadata files, truncated ZIP/gzip streams
and archive identity mismatches fail.

[PyPA's Simple API](https://packaging.python.org/en/latest/specifications/simple-repository-api/)
defines file selection metadata. HTML supports older sources; [Sonatype's PyPI
documentation](https://help.sonatype.com/en/pypi-repositories.html) places JSON
Simple support at Nexus 3.93 and later. [PyPA wheel metadata](https://packaging.python.org/en/latest/specifications/binary-distribution-format/)
provides the embedded package identity.

## Durable publication and authority

Every `project@version` is one publication unit. Planning reads and hashes all
files, verifies archive identity, and freezes the complete file list and
selection snapshot. Execution and recovery re-read source bytes and selection
facts. Every target object is read back in full and hashed before publication;
a matching size, ETag or metadata row does not establish byte integrity.

PyPI requires a persistently identifiable administrator: an active `user:` with
current admin role and no mandatory password change, or an unrevoked, unexpired
standalone `api-key:` with current admin role. Static bootstrap identities,
service accounts and unknown actors are rejected before creating a PyPI batch.
An administrator can use bootstrap authentication to create a managed admin key
first. Current user/key rows are locked and rechecked inside publication;
previously granted authority is not restored from the batch. Source credentials
are used only for read-only requests; refusal during revalidation stops import.

The narrow `PyPIImportStore` shares native publication with the actual database
transaction that validates the batch lease and source connection version. The
version's file references, publication audit and committed entry checkpoint
commit together. A failed quota check, conflicting immutable filename, tombstone,
quarantine, audit write, stale owner or lost database transaction rolls back all
new references. Existing target yank/selection decisions are not overwritten.
Identical retries preserve one publication audit; commit acknowledgement loss
retains the already committed progress for recovery.

Capacity remains the existing explicit target/pool-bound operator evidence.
This change does not turn that evidence into automatic storage measurement.
Unsupported or changed source observations remain visible failures rather than
successful skips. Nexus REST does not offer a transactional repository snapshot;
the worker detects observed drift across bounded repeated reads.

## Reproducible acceptance

Run `make nexus-pypi-migration-e2e` (or the matching script). It uses synthetic
Nexus HTTPS responses and its own PostgreSQL/RustFS Compose project, captures
JSONL results and cleanup receipts, and requires all eight named proofs without
skips. A private `PYPI_MIGRATION_SUBNET` may be supplied for an owned test network
when the local Docker default address pools are exhausted; confirm it does not
overlap an existing network first. The runner deletes only its own project and
temporary client environment.

The runner downloads the fixed pip 26.2 wheel from `files.pythonhosted.org`,
checks its pinned SHA-256 and size against the official PyPI release metadata,
and installs that local wheel without an index. The client test starts with an empty cache and only the target
index configured after the source server has closed. It selects the eligible
wheel, resolves a synthetic dependency, verifies both original byte digests,
and installs/imports them using only downloaded local wheels. A yanked newer
release and a Python-ineligible newer release remain unselected. Source
archive metadata and the full `.tar.gz` download are verified separately;
this is not a source-build acceptance claim.

This slice excludes Proxy/Group asset imports, `.zip`/`.tgz`, executing source
builds, source users/credentials, and new Console flows. PyPI's existing formal
format admission and the parent migration roadmap remain separate from these
specific byte-import proofs.
