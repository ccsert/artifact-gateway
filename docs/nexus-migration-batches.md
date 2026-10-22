# Durable Nexus migration batches

[简体中文](nexus-migration-batches.zh-CN.md) · [Documentation index](README.md)

The administrator API connects [read-only source discovery](nexus-source-discovery.md)
to native Maven release, Raw, OCI, Conan 2 and npm Hosted publication. Batches use
`native-v1`; old policy inputs and historical management interfaces are not
supported. Production cutover, source deletion, conflict overwrites, Proxy caches
and online SNAPSHOT history are outside this workflow. The offline SNAPSHOT
importer remains independent.

Apply migrations `000147_nexus_migration_batches.sql` and
`000148_nexus_migration_scan_diagnostics.sql` and the appended
`000154_nexus_native_migration_formats.sql` and
`000155_nexus_migration_scan_checkpoints.sql` and `000162_nexus_npm_migration.sql` through the standard migration runner. Configure
`GATEWAY_MIGRATION_STORAGE_POOL_ID` with the real object-storage pool identity.
Compose forwards it to every inherited Gateway role. Without it migration writes
are disabled; the rest of Gateway and migration reports remain available. Source
HTTPS, credential encryption, exact private-endpoint allowlists, DNS pinning and
redirect denial are shared with source discovery. No Nexus write API is used.

## Contract and capacity

All paths below are relative to `/api/v2`, require an administrator, and return
`Cache-Control: no-store`.

- `GET /migrations/nexus/capabilities` returns opaque `targetId`, configured
  `poolId`, execution availability, policy and finite retry limit. It exposes no
  endpoint, bucket or credential. The production binding hashes the explicitly
  assembled object-storage endpoint and bucket; custom assembly must provide a
  matching binding for its actual stores instead of assuming a shared pool.
- `POST /migrations/nexus/batches` fixes `sourceId`, exact `sourceVersion`,
  explicit source-to-existing-target UUID mappings, `policyVersion=native-v1`
  and `capacityEvidence`. A background worker completely enumerates the source
  before atomically sealing the manifest and entries. Restart resumes committed
  pages; malformed, oversized, cyclic or changed inventories fail explicitly.
  No partial inventory becomes executable.
- `GET` the collection, `/{batchId}` and `/{batchId}/entries` reads durable state.
  Collection and entry pagination use `limit` (1–100) and UUID `cursor`.
- `POST /{batchId}/pause`, `/resume`, `/retry`, `/cancel` controls the batch.
  Start and controls require `Idempotency-Key`. Controls require the unquoted
  `If-Match` version returned in `ETag`. Reusing a key with different content
  conflicts; identical operations replay their durable response despite stale CAS.

Capacity bytes are **operator-reported external evidence**, not Gateway's
measurement of S3/RustFS. The evidence fixes target and pool, observation and
expiry, free bytes, download/upload/backup/restore peaks and headroom. Every byte
field must be known and nonnegative. Observation cannot be in the future, and
validity cannot exceed one hour. Resume/retry requires a strictly newer
observation. Unknown, insufficient, stale or differently bound evidence prevents
native sessions and object writes. Gateway independently re-reads repository
quotas, native memberships and complete existing object bytes, and uses
`capacityplan.Evaluate`; native publication retains transactional quota checks.
Capacity is rechecked after waiting for format/GC locks. It is evidence, not a
reservation. Temporary/log filesystem observations never stand in for pool space.

## Recovery and identity

PostgreSQL stores immutable batch inputs, the complete manifest hash, per-asset
hashes/validators, entry attempts and checkpoints. One worker owns a batch lease;
different batches can run in parallel. The worker role and `migration` kind must
be enabled, and its format filter must admit every format in that batch. Claims use `SKIP LOCKED`, tokens and epochs.
Expired owners cannot update checkpoints. The actual native publication transaction
takes the same transaction advisory fence as recovery and controls, locks the batch
row and source configuration row in that order, then validates the source version,
live token, expiry and cancellation state before changing visible references.
The source row remains locked through the native transaction commit; a source
update either precedes publication and rejects it, or follows that commit.
Lease expiry is checked again after waiting for the source lock. Losing that physical transaction also aborts publication;
there is no independent session fence whose loss could permit an old publisher.
Format locks cover staging through publication and coordinate GC. A native commit
followed by a lost entry checkpoint remains a full-readback recovery window.

Pause acknowledges a stop on new entries. An already recorded in-flight entry can
finish; `pausing` becomes `paused` after it drains. Cancellation takes the same
transaction publication fence and preserves any committed target. No new publication crosses
a successful cancellation. Failures have at most three attempts; explicit retry
only requeues eligible failed/missing entries, never conflicts or unsupported work.

Raw uses native create-only CAS. Maven freezes a complete release GAV including
POM, main artifact and classifiers, validates required packaging artifacts, and
commits its paths atomically through the native publish session. Checksum sidecars
are generated by Gateway and included in exact capacity accounting; Nexus metadata
is reported as regenerated. A missing POM, missing required main artifact or unknown
packaging is unsupported. A changed release asset set conflicts with an existing
GAV. Unaddressable paths and Raw checksum-resource paths are explicitly unsupported.

OCI maps Nexus `docker` inventories explicitly to native OCI. Each tag or digest
keeps its complete original OCI index/Docker manifest-list, nested manifests,
configs and layers, all platforms and shared objects. Graphs are bounded to depth
8 and 1,024 objects, with 16 MiB per manifest and 64 MiB total manifest pre-read; manifest bodies are read and hashed before sealing, then
revalidated during copying. The entire verified closure and missing tag become
visible atomically. An existing different tag conflicts; the importer never
flattens an index or overwrites a tag. Missing children, descriptor size/hash
mismatches, tag drift and invalid graphs cannot become successful complete
closures. Foreign layers, subject/referrer graphs and unknown media types are
explicit unsupported results. There is no platform-subset option.

Conan uses exact revision storage paths, not Browse projections:
`conans/name/version/user/channel/revisions/RREV/files/file` and, for packages,
`.../packages/PID/revisions/PREV/files/file`. File reads use the source's `v2/`
protocol prefix. Every recipe/package revision must pass the same complete
manifest, expanded-file and identity checks as native Conan publication; source
archives are preserved byte for byte. Recipes publish before their packages.
Fresh sessions own fresh stage keys after expiry, and visible metadata, session
completion and actual-actor audit share the lease-fenced transaction. A quarantined
or tombstoned revision cannot be counted as a verified skip. Conan 1, SCM revisions,
optional metadata and non-tgz archives are unsupported.

Existing success is never sufficient evidence. An identical identity must pass
current source hash/validator checks and complete target identity/size/SHA-256
readback before it is skipped. Different bytes preserve the target and conflict.
Uploaded-but-unrecorded objects and lost commit responses are reconciled by full
readback, without a second visible publication. Source configuration version,
mapping or policy changes require a new batch. Later batches fully enumerate
again: new identities publish, identical identities reverify, and deletions are
reported while retaining their target. There is no timestamp-only watermark.

## npm Hosted package closures

npm uses Nexus Components REST identities (`group`/`name` and `version`) and
cross-checks them against the full packument and the archive's `package.json`.
Tarball filenames never determine a version. One entry contains every version
and dist-tag target of one source package; missing or extra inventory versions,
missing tarballs, wrong SHA-256/shasum/SRI, archive identity mismatches and foreign
or credential-bearing dist URLs fail before publication. Reads share the source's
HTTPS, credential, egress and timeout boundary. Each version has exactly one
tarball, at most 256 MiB; a packument is at most 64 MiB and 1,000 versions, with a 64 MiB aggregate packument budget per batch. SRI
supports SHA-1/256/384/512, selecting the strongest supplied algorithm; a supplied
shasum must match as well. Unknown algorithms or SRI option syntax are refused.

The exact source packument bytes and hash are private ledger state, reobserved
after scanning and immediately before publication. Native packuments are rebuilt
from the original version manifests and frozen tags; native URLs, integrity,
registry revisions and publication timestamps are generated by Gateway. Source
package-level fields outside `name`, `versions` and `dist-tags` remain in the
private receipt and are not replayed into the native registry. A source read is
an observation, not a Nexus transaction snapshot. Stop source writes for final
reconciliation when this distinction matters.

All missing versions and tags commit in one PostgreSQL transaction with the
migration lease/source-version fence, native package coordination, quota and
quarantine checks. Existing identical versions are retained. Missing tags can be
created; a different existing tag, manifest, deprecation or byte identity conflicts
the complete package. Target-only extra versions/tags are retained, and source
removals never delete target content. Source tag moves or deprecation changes
require separate review and explicit target governance before another batch;
migration does not overwrite them automatically. Recovery stages content-addressed
tarballs under native object locks and revalidates full bytes after reopen or
lost acknowledgements; source metadata alone never makes a package visible.

This is a package storage closure, not an automatic crawl of dependency registries.
Select and map every required Hosted package repository explicitly. External,
optional and platform-specific dependency availability still requires target-only
consumer acceptance. Proxy caches and Group bytes remain outside this importer.
`visibleIdentities` counts package closures. Frozen packuments count only toward
`sourceVerificationBytes`; `totalBytes`, `transferredBytes` and `verifiedBytes`
count actual tarball bytes. Private packument receipts are omitted from management asset lists and never receive an uploaded/verified target-byte receipt.

`make nexus-npm-migration-e2e` runs nine mandatory PostgreSQL/RustFS groups with
no skipped tests, including source and target drift, upload/verify/commit failure
recovery, source/lease/native-transaction loss, quota and quarantine protection.
It verifies pinned official npm 11.6.2 from its SHA-512 archive, stops the synthetic
source, reopens native storage, resolves a two-package dependency chain with empty
caches, and checks four exact tarballs. `output/nexus-npm-migration-e2e/<project>/`
retains raw Go JSON, the mandatory-case summary, client output, source head/tree
and cleanup receipts. The script creates and removes only its owned Compose
project; all owned containers, volumes and networks must be absent afterward.
These fixtures do not establish acceptance for a real business Nexus version or
inventory and do not authorize production traffic changes.

## Progress and limits

`metadataDiscovered` counts inventory records. Discovery-provided required hashes
are reused. Missing SHA-256 (or Maven SHA-1/MD5 needed for derived sidecars) triggers
bounded source verification; OCI graph manifest pre-reads and complete npm archive verification are always required. Its actual reads are durably counted separately as
`sourceVerificationBytes`, including incomplete scans. Raw, Maven and OCI verification streams hashes without target writes. npm uses a private temporary archive spool for package identity validation and removes it after the bounded read.

`transferredBytes` counts copy-stage source body bytes, including retries and
revalidation of existing identities. `verifiedBytes` counts unique primary target
bytes with full readback evidence; retries do not inflate it. `visibleIdentities`
counts committed or readback-confirmed native Raw paths, Maven GAVs, OCI references, Conan revisions and npm package closures separately.
`totalBytes` is null until the complete manifest is sealed, then counts unique
supported primary bytes. Unsupported and source-deleted entries remain explicit
report rows. These counters do not constitute official-client acceptance.
Unsupported paths use synthetic `/unsupported/<decoded source path>` report
identities, disjoint from native relative Raw paths and Maven GAVs. Duplicate
source paths fail discovery; a complete rescan emits one report per unsupported
path in that batch without renaming or discarding a legitimate native identity.

A failed scan persists `lastScanFailure` independently of executable entries. It
records the bounded stage, selected repository scope, exact known source repository,
asset path and identity, public error code and occurrence time, and survives a
process restart. Before an asset is known its path and identity remain empty; the
metadata stage identifies the selected repository scope without leaking upstream
URLs or credentials. A partial scan still has no executable manifest or entries
and `totalBytes=null`. A later complete scan clears the old failure diagnostic.

Migration inventory scanning uses a dedicated paged source reader. Its explicit
budgets are ten repositories, 10,000 recorded pages, 100,000 assets and 64 MiB
of cumulative asset wire metadata, including the second observation; each page is bounded to 2 MiB and 30 seconds. The interactive
discovery preview retains its smaller 100-page/10,000-asset budget. Neither path
silently truncates an inventory when its budget is exhausted.

Private PostgreSQL checkpoints commit each page and its next cursor together,
bound to the source version, selected repository shapes, mappings and policy.
Restart or a lost acknowledgment resumes the committed cursor. Paused/cancelled
batches and expired workers cannot advance it. Once enumeration ends, a second
paged observation must match every recorded page before the complete manifest
can be sealed. Cursor expiry, loops, duplicate paths, changed source observations
or budget exhaustion fail explicitly; a new batch is required for source drift.
These observations do not make the Nexus REST API a transactional snapshot.
Before sealing there are no executable entries and `totalBytes` remains null,
even when `metadataDiscovered` has advanced.

Each file remains limited to 1 GiB and 30 seconds, read with a size-plus-one
bound and full hash. Downloads use immediately unlinked private temporary files;
recovery retries whole files, with no promised range/multipart resume.

`make nexus-inventory-scan-e2e` verifies more than 100 pages and 10,000 assets,
restart and lost-acknowledgment recovery, lease fencing, pause/cancel, source
binding and scan-budget rejection against an owned PostgreSQL/RustFS fixture.
Required tests cannot be skipped, and its owned resources must be removed.

`make integration-test` includes the domain PG package and exercises
real PostgreSQL/RustFS crash windows, competing/expired workers, pause/cancel,
capacity, changed source and native protocol readback. The separate
`make nexus-migration-client-e2e` gate disables the source and uses an empty
artifact cache and target-only resolution. It fixes official Maven 3.9.16
(archive SHA-512
`831a8591fe20c8243b1dbe7d71e3244f31d1665b0804b2e825e38cbbe5ce0cafb8338851f90780735568773e0a6cd07bbec107cda0b896b008b861075358b6f6`)
and builds official curl 8.14.1 from source (SHA-256
`6766ada7101d292b42b8b15681120acd68effa4a9660935853cf6d61f0d984d4`)
in a private temporary directory. Both clients verify the complete expected
inventory, size and SHA-256 through Gateway alone. This loopback HTTP evidence
does not prove production TLS or authorize a cutover. Passing either gate is not
production migration approval.

The additional `make nexus-oci-migration-e2e` gate pins official crane 0.22.1,
verifies all platforms, amd64 and arm64 from separate empty layouts for OCI index,
Docker list and nested-index fixtures, and exercises PostgreSQL lease fencing,
atomic closure publication, acknowledgement recovery and GC races. `make conan-e2e`
also requires the Conan migration fixtures; `make nexus-conan-migration-e2e` runs
them independently with pinned Conan 2.21.0, real PostgreSQL/RustFS restarts, two
historical revisions and target-only installs from new client homes. Each owned
fixture verifies its cleanup. These are synthetic Nexus REST inventories and
standard protocol sources; actual deployed Nexus versions, business inventories,
production trust/capacity and final cutover require their own acceptance evidence.
