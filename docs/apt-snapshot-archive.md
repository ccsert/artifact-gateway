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

The command runs before server configuration is loaded. It does not connect to a database, object store, or signer, or extract filesystem paths. Success exits 0 and writes JSON containing `integrity: verified`, `signatures: not_checked`, snapshot/repository IDs, and package/asset counts. Archive errors exit 1; invalid command usage exits 2.

`integrity: verified` establishes complete bytes, Debian package identity, manifest references, and Release index closure. It does not establish signer trust. Separately verify InRelease and Release.gpg using an operator-owned public-only keyring and a trusted fingerprint policy. A fingerprint supplied by the archive is not a trust root.

## HTTP and Archive Contract

- `GET /api/v2/repositories/{repositoryId}/apt/snapshots/{snapshotId}/archive` requires `repositories:admin`. Ordinary readers, writers, and anonymous callers cannot download archives.
- Only `visible` and `retired` snapshots can be exported. Cross-repository requests return 404; unpublished states or corrupt objects return 409. Archives include management metadata and package contents, so responses use `Cache-Control: private, no-store`.
- The media type is `application/vnd.artifact-gateway.apt-snapshot.v1+tar`. Responses include a download filename and exact `Content-Length`. All objects and Release references are checked before headers are sent; a streaming failure aborts the HTTP response. Clients must check download completion and run archive verification.
- The first tar entry is `manifest.json`, followed by sorted, deduplicated `objects/sha256/<hex>` entries. Repeated exports of an unchanged snapshot state are byte-identical. A visible-to-retired transition changes the manifest state while preserving package and signature assets.
- The manifest schema is `artifact-gateway.dev/apt-snapshot-export/v1`. It includes snapshot identity and signing evidence, package identity/publisher/timestamps, and protocol-path digests and sizes. It excludes internal object-store keys, signer credentials, and private keys.
- Verification rejects non-regular files, unknown/duplicate/missing objects, unsorted objects, size/digest mismatches, unknown schemas/fields, invalid package identities, missing indexes, truncated endings, and trailing data. Limits are a 128 MiB manifest, 10,000 packages, 50,016 assets, and 50,000 distinct objects. Objects are at most 1 GiB; Release/InRelease are at most 16 MiB and Release.gpg is at most 1 MiB.

This stage provides no archive import endpoint and does not change snapshot visibility, retention, or database migrations. Existing paired PostgreSQL/RustFS restoration continues to follow the [recovery runbook](recovery-runbook.md).

## Staged Legacy Migration

Parent [#30](https://github.com/ccsert/artifact-gateway/issues/30) uses these fixed references:

- Current mainline baseline: `eb302a1bdf3bb9f3cd0ba20a7dd30e0895362323`.
- Preserved branch: `codex/apt-hosted-complete-20260818` at `02721dbf058599f54b4968032463885e0067b678`, following implementation commit `7fec72de` and its documentation commit.

| Legacy area | Decision and current contract |
| --- | --- |
| export.go / archive.go / export API | [#36](https://github.com/ccsert/artifact-gateway/issues/36): retain deterministic archives, adapt current package membership, and strengthen streaming failure, length, terminator, and package-identity checks. Add snapshot-ID asset reads to Memory/PostgreSQL without a migration. |
| import.go / trusted recovery | [#37](https://github.com/ccsert/artifact-gateway/issues/37): separately review exact same-repository recovery, both signature checks, quotas, idempotency, concurrency, and object cleanup. Integrity verification is not signature trust. |
| lifecycle.go / snapshot.go / lifecycle persistence | [#38](https://github.com/ccsert/artifact-gateway/issues/38): separately port deletion, retention, and restoration through newly signed snapshots. Never overwrite mainline migrations 000107/000108; append migrations after the then-current mainline when needed. |
| promotion.go / replication.go / scanning and quarantine | [#39](https://github.com/ccsert/artifact-gateway/issues/39): separate PRs against current scan identity, admission, and Worker contracts. Rebuild and sign destination metadata instead of copying the source Release as destination authority. |
| Old OpenAPI, generated code, docs, and public capability claims | Regenerate or rewrite against current mainline. Discard the old public Hosted release-candidate claim; preserve H3/KMS/HSM and Group aggregate-signing boundaries. Do not revert current error wrapping, Go/Maven, or Console fixes. |

Completing stage one does not delete the old branch. Cleanup requires migration or explicit retirement of every valuable behavior and a recorded recovery SHA.

## Validation Entrypoints

The Hosted gate under `make native-apt-e2e` uses a real Debian client for publication, repeated export, retired-snapshot export, paired PostgreSQL/RustFS backup/restore, and offline-signer export, comparing archive bytes before and after restoration. It then materializes a fixture directory from the archive and verifies both signatures with the operator public key before `apt-get update` and installation in a Debian container with `--network none`. This proves archive usability, not Gateway archive import.

`make integration-test` exercises cross-instance PostgreSQL/RustFS export and integrity verification. Go tests cover authorization/repository isolation, streaming interruption, corrupt/missing objects, duplicate/concatenated/truncated archives, and the offline CLI. OpenAPI and generated clients are rechecked against current mainline.
