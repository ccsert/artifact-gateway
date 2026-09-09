# APT Hosted snapshot lifecycle

[简体中文](apt-hosted-lifecycle.zh-CN.md) | [Documentation index](README.md)

The APT Hosted operator preview supports dedicated package deletion, recovery, retention planning and snapshot pruning. Every membership change generates complete Packages indices, Release and both signatures before one transaction switches visibility. APT remains publicly advertised as Proxy/Group; production KMS/HSM custody remains a separate gate. Target-signed distribution is covered by the [distribution preview](apt-hosted-distribution.md).

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
- Current Release/Packages routes use the visible snapshot. Immutable pool and by-hash routes remain readable while a visible or retired snapshot references them, allowing clients with cached old Release files to finish. When quarantine read enforcement is enabled, it takes precedence over this availability grace.
- Removing the final package preserves each existing component/architecture scope with signed empty Packages, gzip and by-hash indices. The empty Packages representation has zero bytes.
- Prune accepts `{"snapshotIds":["eligible retired UUID"]}`, at most 100 IDs, and atomically removes the whole validated selection's assets and membership. The pruned identity and signing evidence remain. Visible snapshots are never eligible; repeated pruned IDs are safe.
- `{"snapshotIds":[]}` only sweeps expired deletions. Revisions remain protected by live/building snapshot membership, any other unexpired deletion, or staging sessions created in the last 24 hours. Preparing archive object intents always prevent physical deletion.
- Reclamation uses durable jobs, object locks, global reference checks and worker retries. A successful prune response means references were processed, not that physical deletion has finished. Shared objects and other suites remain protected.
- Capacity is released only when package revisions are removed; recoverable packages remain charged. Current generated metadata counts unique objects.

Purged identity barriers and history remain; re-uploading that identity cannot bypass deletion. After recovery expires, restore a consistent database/object backup in a separate validation environment or publish a new package version. A single snapshot archive has no deletion/queue history and is not a full lifecycle backup.

The Console exposes these explicit operations under a Hosted APT Repository's **Signed snapshots** tab. Repository administrators enter an exact suite, inspect the current signing fingerprint and snapshot history, select packages for deletion, preview retention, or recover an eligible deletion. The review lists affected packages and the fixed base snapshot; retention apply sends the exact preview candidate IDs. A conflict requires refresh and a new preview. A failed or uncertain apply can reuse its existing idempotency key while the review remains open. A refresh failure keeps the last evidence visible and disables operations until refresh succeeds.

Snapshot history permits pruning only retired snapshots whose grace deadline has elapsed, with explicit confirmation. It does not claim physical byte reclamation has completed. The generic scheduled retention-policy executor remains unavailable for APT. The Console does not provide first publication or archive import/export forms.

The lifecycle response includes each package's server-generated `poolPath`, used for protocol reads and distribution. It is distinct from `revision.canonicalIdentity`, which is `package@version#architecture`. The Console never derives a pool path from that package identity. Run matching API and Console versions when using these operations.

## Upgrade and verification

Migration `000117_native_apt_lifecycle.sql` is additive and does not rewrite historical migrations. Take a consistent PostgreSQL/RustFS backup and stop all old writer nodes and workers before migration. Do not mix binaries that do not enforce deletion barriers.

Existing nonempty v1 archives remain readable without byte changes. Empty snapshots use the v1 archive structure, but older validators reject an empty package set. Older binaries also do not understand pruned state or deletion barriers. Down migration explicitly refuses to run; rollback requires restoring the pre-upgrade database, objects and matching binary, not merely switching the image.

Run `go test ./internal/aptpublication ./internal/app ./internal/repository`, isolated `make integration-test`, and `make native-apt-e2e`. The Debian gate exercises signer failure/retry, signed empty indices, retained pool downloads, retention and recovery installations, followed by consistent backup recovery and trusted archive recovery without a signer.

## Scanning and quarantine

APT Hosted scan identities are `Repository + apt + canonical pool path + SHA-256` for `.deb` files in current visible snapshots. All suites share governance for the same pool identity. Staged packages, retired-only references, Release/Packages and suite-qualified aliases are not scan identities. Quarantined packages remain scannable for investigations. The adapter streams the complete digest/size-verified `.deb`; the existing Worker merges intelligence without replacing publisher signatures or provenance. `autoScanOnPublish` enqueues after snapshot commit. Explicit scanning and reconciliation are also available; scheduling failures are audited and reconciliation can repair them.

Use `PUT /artifact-quarantine?coordinate=<encoded pool path>&digest=<SHA-256>` with `If-Match: 0` to create a decision, then its current version to transition it. The body contains `state` and `reason`. Read enforcement is separately versioned and off by default: enable `PUT /quarantine-read-policy` with `If-Match: 1` and `{"version":"1","enabled":true}`. Both controls require Repository administrator access and retain versions and audit evidence.

- With read enforcement off, existing signed reads remain compatible. New publication, lifecycle restore and new archive import reject quarantined members. Exact archive replay never reactivates an old snapshot.
- With enforcement on, the quarantined pool GET/HEAD returns 403. Every complete signed metadata view containing that package also returns 403: Packages, gzip, Release, both signatures, and retired by-hash indices. Conditional requests and Range pass the same check. Other packages' pool downloads remain available.
- Enforcement denies a complete signed view; it never filters Packages during a read or waits for an online signer to quarantine. To keep distributing the remaining packages, delete the quarantined member through the lifecycle API to generate a new signed view. Old indices remain governed.
- Release only restores reads of existing lifecycle references. It does not undo deletion, switch the current snapshot or change signed bytes. Deleted packages still need an explicit restore within their recovery window.
- Publication checks governance when loading packages and immediately before invoking the signer. Its final transaction locks the same repository row as quarantine transitions and rechecks all pool identities. A quarantine during signing returns `409 artifact_quarantined` and preserves the old visible snapshot.

Configure an external service capable of analyzing `.deb` and include `apt` in `GATEWAY_SCANNER_FORMATS`. The bundled reference Trivy filesystem scanner still rejects APT; an unanalyzed archive must not become a clean report. See the [scanner contract](artifact-scanner-contract.md). APT Groups still accept only Proxy members, preventing Hosted Group bypass. Aggregate signing and KMS/HSM remain separate work. See the [target-signed distribution preview](apt-hosted-distribution.md).

No new database fields are required. Stop old binaries before upgrading all nodes: they do not enforce this APT governance boundary. Verification covers Memory/PostgreSQL/RustFS admission during signing, retired reads, delete/release/restore, and a real Debian client denied installation using cached indices, then installing after release.
