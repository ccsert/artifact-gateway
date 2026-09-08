# APT Hosted snapshot lifecycle

[简体中文](apt-hosted-lifecycle.zh-CN.md) | [Documentation index](README.md)

The APT Hosted operator preview supports dedicated package deletion, recovery, retention planning and snapshot pruning. Every membership change generates complete Packages indices, Release and both signatures before one transaction switches visibility. APT remains publicly advertised as Proxy/Group; production KMS/HSM custody, quarantine and distribution are separate gates.

## Management workflow

All routes below are relative to `/api/v2/repositories/{repositoryId}` and require repository administrator access.

| Route | Purpose |
| --- | --- |
| `GET /apt/lifecycle?suite=stable` | Published snapshot history and `prunableAfter`, current package session IDs, deletion records and recovery deadlines |
| `POST /apt/lifecycle/preview` | Read-only membership plan; works without a configured signer |
| `POST /apt/lifecycle` | Apply with a required `Idempotency-Key`, rebuilding and signing a new snapshot |
| `POST /apt/snapshots/prune` | Remove selected retired snapshot references and sweep expired unreferenced deletions |

A delete request contains `suite`, `expectedSnapshotId`, `action: delete` and `publicationSessionIds` taken from the current lifecycle view. Restore uses `action: restore` with `deletionIds` instead. It adds those packages to the current complete membership and signs a new view; it never reactivates the old snapshot. Ordinary publication and new archive imports cannot bypass an active deletion barrier.

For retention, preview `action: retention`, `keepLatest: 1`, `olderThanDays: 30`. Candidates are grouped by component, package name and architecture. Only uploads both older than the threshold and beyond the retained count qualify. “Latest” means original upload time, not lexicographical or Debian version ordering; restoration does not reset that age. For apply, copy the preview's `removeSessionIds` exactly into `publicationSessionIds`. A changed candidate set or current snapshot returns 409 and requires a new preview. An empty plan needs no apply and returns 409 if submitted.

All changes bind `expectedSnapshotId`. The successful snapshot, request digest and idempotency result commit together. Same actor/key/payload retries return the original result, even after retirement or pruning; different payloads return 409. Signing or object-write failures preserve the current view and permit a same-key retry with a fresh build ID and increasing sequence. Sequence gaps are expected.

## Recovery and reclamation

- Package recovery lasts seven days. After the deadline the restore API refuses recovery even if bytes have not yet been physically deleted.
- Snapshots remain available for at least 24 hours after retirement. Migration starts a new full grace period for legacy retired snapshots whose actual retirement time is unknown.
- Current Release/Packages routes use the visible snapshot. Immutable pool and by-hash routes remain readable while a visible or retired snapshot references them, allowing clients with cached old Release files to finish. This is availability grace, not security quarantine.
- Removing the final package preserves each existing component/architecture scope with signed empty Packages, gzip and by-hash indices. The empty Packages representation has zero bytes.
- Prune accepts `{"snapshotIds":["eligible retired UUID"]}`, at most 100 IDs, and atomically removes the whole validated selection's assets and membership. The pruned identity and signing evidence remain. Visible snapshots are never eligible; repeated pruned IDs are safe.
- `{"snapshotIds":[]}` only sweeps expired deletions. Revisions remain protected by live/building snapshot membership, any other unexpired deletion, or staging sessions created in the last 24 hours. Preparing archive object intents always prevent physical deletion.
- Reclamation uses durable jobs, object locks, global reference checks and worker retries. A successful prune response means references were processed, not that physical deletion has finished. Shared objects and other suites remain protected.
- Capacity is released only when package revisions are removed; recoverable packages remain charged. Current generated metadata counts unique objects.

Purged identity barriers and history remain; re-uploading that identity cannot bypass deletion. After recovery expires, restore a consistent database/object backup in a separate validation environment or publish a new package version. A single snapshot archive has no deletion/queue history and is not a full lifecycle backup.

Retention and pruning are explicit operator API actions in this phase. The generic scheduled retention-policy executor, Console lifecycle forms, quarantine, promotion and replication are not enabled for APT by this work.

## Upgrade and verification

Migration `000117_native_apt_lifecycle.sql` is additive and does not rewrite historical migrations. Take a consistent PostgreSQL/RustFS backup and stop all old writer nodes and workers before migration. Do not mix binaries that do not enforce deletion barriers.

Existing nonempty v1 archives remain readable without byte changes. Empty snapshots use the v1 archive structure, but older validators reject an empty package set. Older binaries also do not understand pruned state or deletion barriers. Down migration explicitly refuses to run; rollback requires restoring the pre-upgrade database, objects and matching binary, not merely switching the image.

Run `go test ./internal/aptpublication ./internal/app ./internal/repository`, isolated `make integration-test`, and `make native-apt-e2e`. The Debian gate exercises signer failure/retry, signed empty indices, retained pool downloads, retention and recovery installations, followed by consistent backup recovery and trusted archive recovery without a signer.
