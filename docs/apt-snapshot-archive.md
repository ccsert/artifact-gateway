# APT Signed Snapshot Archives

[简体中文](apt-snapshot-archive.zh-CN.md) | [Documentation index](README.md)

The APT Hosted operator preview supports exporting published signed snapshots and checking archive integrity offline without a database or signer. Archives preserve the original `.deb`, Packages/gzip/by-hash indexes, Release, InRelease, and Release.gpg bytes without signing again. Production custody and public compatibility boundaries remain in [APT Hosted signing](apt-hosted-signing.md).

## Export and Verification

Download a fixed snapshot ID as a repository administrator. Obtain the current ID from `currentSnapshot.id` in the repository's `apt/signing-state` response, or use a previously recorded ID to export a superseded `retired` snapshot.

```sh
curl --fail --show-error \
  --header "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  "$GATEWAY_URL/api/v2/repositories/$REPOSITORY_ID/apt/snapshots/$SNAPSHOT_ID/archive" \
  --output snapshot.tar
gateway apt-snapshot verify snapshot.tar
```

Standard input is also supported:

```sh
gateway apt-snapshot verify - < snapshot.tar
```

The command runs before server configuration is loaded. It does not connect to a database, object store, or signer, or extract filesystem paths. Success exits 0 and writes JSON containing `integrity: verified`, `signatures: not_checked`, `archiveDigest: sha256:<hex>`, snapshot/repository IDs, and package/asset counts. Archive errors exit 1; invalid command usage exits 2.

`integrity: verified` establishes complete bytes, Debian package identity, manifest references, and Release index closure. It does not establish signer trust. Separately verify InRelease and Release.gpg using an operator-owned public-only keyring and a trusted fingerprint policy. A fingerprint supplied by the archive is not a trust root.

## HTTP and Archive Contract

- `GET /api/v2/repositories/{repositoryId}/apt/snapshots/{snapshotId}/archive` requires `repositories:admin`. Ordinary readers, writers, and anonymous callers cannot download archives.
- Only `visible` and `retired` snapshots can be exported. Cross-repository requests return 404; unpublished states or corrupt objects return 409. Archives include management metadata and package contents, so responses use `Cache-Control: private, no-store`.
- The media type is `application/vnd.artifact-gateway.apt-snapshot.v1+tar`. Responses include a download filename and exact `Content-Length`. All objects and Release references are checked before headers are sent; a streaming failure aborts the HTTP response. Clients must check download completion and run archive verification.
- The first tar entry is `manifest.json`, followed by sorted, deduplicated `objects/sha256/<hex>` entries. Repeated exports of an unchanged snapshot state are byte-identical. A visible-to-retired transition changes the manifest state while preserving package and signature assets.
- The manifest schema is `artifact-gateway.dev/apt-snapshot-export/v1`. It includes snapshot identity and signing evidence, package identity/publisher/timestamps, and protocol-path digests and sizes. It excludes internal object-store keys, signer credentials, and private keys.
- Verification rejects non-regular files, unknown/duplicate/missing objects, unsorted objects, size/digest mismatches, unknown schemas/fields, invalid package identities, missing indexes, truncated endings, and trailing data. Limits are a 128 MiB manifest, 10,000 packages, 50,016 assets, and 50,000 distinct objects. Objects are at most 1 GiB; Release/InRelease are at most 16 MiB and Release.gpg is at most 1 MiB.

## Trusted Same-Repository Restore

Before storing a backup, run `gateway apt-snapshot verify snapshot.tar` and save the returned `archiveDigest` in a separate trusted backup record. The OpenPGP signatures cover Release and its indexes, **not** the archive manifest's repository UUID, snapshot ID or publisher metadata. The independently saved whole-archive SHA-256 receipt pins those fields. Never calculate an authorization receipt from an untrusted upload at restore time.

Configure both settings on the recovering Gateway, using an operator-owned public-only keyring:

```sh
GATEWAY_APT_RESTORE_TRUSTED_FINGERPRINTS=<trusted-primary-fingerprint>
GATEWAY_APT_RESTORE_TRUSTED_PUBLIC_KEYS_FILE=/run/secrets/apt-restore-public-keys.asc
```

Restore is disabled by default. Fingerprints and keyring must match exactly; private keys are rejected. Mount the file read-only in a Compose override when using containers. These settings are independent of `GATEWAY_APT_SIGNER_*`; no signer endpoint, token, private key, or signing RPC is needed. The existing RSA public-key policy and controlled rotation keyring limits apply.

```sh
# EXPECTED_ARCHIVE_DIGEST comes from the independently saved backup record.
curl --fail-with-body --show-error \
  --request POST \
  --header "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  --header 'Content-Type: application/vnd.artifact-gateway.apt-snapshot.v1+tar' \
  --header "X-Artifact-Archive-Digest: $EXPECTED_ARCHIVE_DIGEST" \
  --data-binary @snapshot.tar \
  "$GATEWAY_URL/api/v2/repositories/$REPOSITORY_ID/apt/snapshots/restore"
```

The target must already be an active APT Hosted repository with the **original repository UUID**; recreating its name with a new UUID is insufficient. Preserve repository configuration and authorization separately using the [recovery runbook](recovery-runbook.md).

Signer identity must match the verified public key UID; the historical reference signer label `Name <email>` without its UID comment is also accepted and preserved.

Restore checks the receipt, archive structure, every object and `.deb`, both InRelease and Release.gpg against the independent keyring, and signer evidence. It reconstructs the current v1 publisher's canonical Packages, gzip, by-hash and Release bytes from the actual packages and compares the entire signed index set. Arbitrary third-party repository archives and equivalent but differently encoded indexes are not accepted. A digest-valid manifest cannot add an unsigned package.

Object intents and capacity reservations are durable before content writes. Package revisions, membership, pool paths, snapshot visibility and the success audit become visible in one database transaction. Failure leaves no partial package metadata or new visible snapshot; abandoned objects are collected by the existing APT lifecycle worker. A crashed attempt expires after one hour, fenced by the snapshot lock, and each retry has a separate attempt ID. Active restores protect their objects from old cleanup jobs.

- Administrator permission is required. Both the receipt and exact media type are mandatory.
- A previously absent snapshot becomes visible only if its suite sequence exceeds the currently visible sequence and has no identity, coordinate or pool-path conflicts. An existing exact visible or retired snapshot can be replayed to repair objects; retired snapshots are not promoted. Re-export preserves the archive bytes when the snapshot state has not changed.
- Quotas are checked before object writes and again at commit; pending restore reservations also constrain ordinary APT uploads and publications. Each successful attempt records `apt.repository_snapshot.restore` with the receipt, attempt ID and immutable signing evidence.
- Each Gateway instance permits two concurrent restore requests and a 64 GiB tar limit. Provision temporary disk accordingly; spools are removed after success or failure. Object, manifest and signature bounds above also apply; normalized package control metadata is capped at 128 MiB.
- Errors include 400 for malformed or corrupt archives, 404 for a different repository, 409 for disabled trust or metadata/state conflicts, 412 for receipt mismatch, 413 for size limits, 415 for media type, 422 for untrusted signatures, 429 for restore concurrency, and 507 for quota rejection.

Migration `000116_native_apt_archive_restore.sql` appends restore attempts and object intents; historical migrations are unchanged. Snapshot deletion, retention and re-signed restoration are described in the [lifecycle guide](apt-hosted-lifecycle.md); promotion/replication remain a later stage. APT Hosted remains an operator preview; this does not complete production KMS/HSM custody.

## Staged Legacy Migration

Parent [#30](https://github.com/ccsert/artifact-gateway/issues/30) uses these fixed references:

- Stage-one starting baseline: `eb302a1bdf3bb9f3cd0ba20a7dd30e0895362323`.
- Preserved branch: `codex/apt-hosted-complete-20260818` at `02721dbf058599f54b4968032463885e0067b678`, following implementation commit `7fec72de` and its documentation commit.

| Legacy area | Decision and current contract |
| --- | --- |
| export.go / archive.go / export API | [#36](https://github.com/ccsert/artifact-gateway/issues/36): retain deterministic archives, adapt current package membership, and strengthen streaming failure, length, terminator, and package-identity checks. Add snapshot-ID asset reads to Memory/PostgreSQL without a migration. |
| import.go / trusted recovery | [#37](https://github.com/ccsert/artifact-gateway/issues/37): exact same-repository recovery with independent backup receipts and public keys, canonical signed index checks, atomic metadata, quotas, replay, concurrency and durable object cleanup. |
| lifecycle.go / snapshot.go / lifecycle persistence | [#38](https://github.com/ccsert/artifact-gateway/issues/38): separately port deletion, retention, and restoration through newly signed snapshots. Never overwrite mainline migrations 000107/000108; append migrations after the then-current mainline when needed. |
| promotion.go / replication.go / scanning and quarantine | [#39](https://github.com/ccsert/artifact-gateway/issues/39): separate PRs against current scan identity, admission, and Worker contracts. Rebuild and sign destination metadata instead of copying the source Release as destination authority. |
| Old OpenAPI, generated code, docs, and public capability claims | Regenerate or rewrite against current mainline. Discard the old public Hosted release-candidate claim; preserve H3/KMS/HSM and Group aggregate-signing boundaries. Do not revert current error wrapping, Go/Maven, or Console fixes. |

Completing stage one does not delete the old branch. Cleanup requires migration or explicit retirement of every valuable behavior and a recorded recovery SHA.

## Validation Entrypoints

The Hosted gate under `make native-apt-e2e` uses a real Debian client for publication, repeated export, retired-snapshot export, paired PostgreSQL/RustFS backup/restore, and offline-signer export, comparing archive bytes before and after restoration. It then materializes a fixture directory from the archive and verifies both signatures with the operator public key before `apt-get update` and installation in a Debian container with `--network none`. It additionally resets PostgreSQL and RustFS to an empty original-repository baseline, disables the signer, restores the archive twice through the management API, checks byte-identical re-export and installs from the restored Gateway.

`make integration-test` exercises cross-instance PostgreSQL/RustFS export, interrupted restore reclamation, concurrent restore, exact replay and object repair. Go tests cover authorization/repository isolation, streaming interruption, corrupt/missing objects, duplicate/concatenated/truncated archives, and the offline CLI. OpenAPI and generated clients are rechecked against current mainline.
