# RustFS Migration

Artifact Gateway treats object storage as an S3-compatible byte store. The
application-facing `GATEWAY_S3_*` contract remains unchanged; local Compose,
integration tests, and Kubernetes fixtures use RustFS as the implementation.

The pinned baseline is `rustfs/rustfs:1.0.0-beta.12` with image digest
`sha256:41fe89380f4120a337790c02af192c3fe7bb55c3edc2e6e9357b487b47c6ab21`.
RustFS is still a beta dependency. Production adoption therefore requires a
canary, an explicit rollback window, and the release gates below. See the
[official container deployment documentation](https://docs.rustfs.com/en/installation/container/docker)
and [upstream releases](https://github.com/rustfs/rustfs/releases).

## Non-negotiable boundaries

- Never mount a MinIO data directory into RustFS. Their internal on-disk layouts
  are not the migration contract.
- Copy objects through S3 while MinIO and RustFS use separate storage volumes.
- Keep `RUSTFS_RPC_SECRET` independent from `RUSTFS_SECRET_KEY`. Do not reuse
  Gateway, database, or identity-provider credentials.
- Preserve the exact bucket, object key, bytes, content type, and user metadata.
  Gateway's verified digest is stored as S3 user metadata and is part of the
  object contract.
- Keep the source MinIO volume read-only and recoverable until the rollback
  window has closed.

## Staged cutover

1. **Inventory.** Record the source endpoint, bucket, object count, total bytes,
   Gateway version, PostgreSQL backup, MinIO backup, and current readiness
   evidence. Do not record credentials.
2. **Provision.** Start the pinned RustFS image with a new empty volume, a new S3
   access/secret pair, and an independent RPC secret. Do not change Gateway yet.
3. **Initial copy.** Use the repository-native streaming migration command. It
   reads credentials only from environment variables, never accepts secret
   flags, preserves durable HTTP metadata plus all S3 user metadata, and emits
   a JSON manifest fingerprint without credentials:

   ```sh
   export S3_MIGRATION_SOURCE_ACCESS_KEY='<from-approved-secret-store>'
   export S3_MIGRATION_SOURCE_SECRET_KEY='<from-approved-secret-store>'
   export S3_MIGRATION_TARGET_ACCESS_KEY='<from-approved-secret-store>'
   export S3_MIGRATION_TARGET_SECRET_KEY='<from-approved-secret-store>'

   go run ./cmd/s3-migrate inventory \
     --source-endpoint http://127.0.0.1:19000 \
     --source-bucket gateway-cache

   go run ./cmd/s3-migrate copy \
     --source-endpoint http://127.0.0.1:19000 \
     --source-bucket gateway-cache \
     --target-endpoint http://127.0.0.1:19001 \
     --target-bucket gateway-cache
   ```

   The `copy` operation always performs a complete verification after upload.
   It does not delete target keys; an unexpected target key fails verification.
   Supply credentials through an approved secret store and unset the four
   variables when finished. Do not use `mc mirror` for this migration: its
   object copy does not preserve arbitrary S3 user metadata, including
   Gateway's verified digest.
4. **Verify the copy.** Retain the `copy` JSON evidence, then independently run
   `s3-migrate verify` with the same endpoint and bucket flags. Verification
   downloads every source and target object, compares the exact key set, hashes
   the bytes, and compares size, content type, content encoding, content
   disposition, content language, cache control, and normalized S3 user
   metadata. Any extra key or missing digest metadata is a failed migration.
5. **Freeze writes.** Stop Gateway API, scheduler, worker, and scanner roles.
   Confirm no promotion, replication, upload, cache, scan, reclaim, or webhook
   worker can write object state. PostgreSQL remains the authoritative metadata
   snapshot during this window.
6. **Final copy.** Repeat `copy`, then independently run `verify` against the
   frozen source. Do not use the destructive `mirror` operation during forward
   cutover; it is reserved for the separately reviewed rollback procedure below.
7. **Record cutover confirmation.** Retain the frozen-copy command output,
   source/destination object count and byte totals, user-metadata sample, and
   rollback owner in the release record. Only after those checks pass, set
   `GATEWAY_RUSTFS_MIGRATION_CONFIRMED=1` for Compose or
   `K8S_LOCAL_RUSTFS_MIGRATION_CONFIRMED=1` for the local Kubernetes helper.
   These flags acknowledge retained evidence; they are not copy tools and are
   not substitutes for `s3-migrate verify`.
8. **Switch.** Change only `GATEWAY_S3_ENDPOINT`, `GATEWAY_S3_ACCESS_KEY`, and
   `GATEWAY_S3_SECRET_KEY`, then start Gateway against RustFS. Keep the bucket
   name unchanged unless the completed copy used a deliberately different name.
9. **Canary.** Require `/readyz=204`, then verify authenticated GET/HEAD/Range,
   one immutable publication, promotion, replication, scan byte retrieval, and
   delayed reclaim. Review object-store errors and latency before reopening all
   traffic.
10. **Close the window.** Retain the frozen MinIO volume and credentials until the
   approved rollback period ends. Revoke them and delete the old storage only as
   a separate, explicitly approved destructive change.

## Rollback

Before new writes are admitted on RustFS, rollback only requires stopping
Gateway and restoring the former S3 endpoint and credentials. After any RustFS
write has succeeded, freeze Gateway and mirror the RustFS delta back to MinIO
before reverting; otherwise PostgreSQL may reference bytes that exist only in
RustFS. Because a post-cutover deletion must also be reflected in the old
backend, the rollback uses the explicit destructive mirror operation while both
write paths are frozen:

```sh
go run ./cmd/s3-migrate mirror \
  --source-endpoint http://127.0.0.1:19001 \
  --source-bucket gateway-cache \
  --target-endpoint http://127.0.0.1:19000 \
  --target-bucket gateway-cache \
  --delete-target-extras
```

`mirror` copies current source objects, deletes only target keys absent from the
source, and then performs exact verification. Never run it against a writable
source or target. Retain its JSON evidence and independently re-run `verify`
before reopening traffic.

## Local Kubernetes upgrade

The local helper refuses to apply the RustFS overlay when it detects the legacy
`minio` StatefulSet or its `data-minio-0` PVC unless the verified-cutover
confirmation above is present. For a disposable stack, `make kubernetes-local-down`
deletes the namespace and all local data; a subsequent
`make kubernetes-local-up` creates RustFS. For data that must survive, deploy
RustFS side by side and use the staged S3 copy above instead of deleting the
namespace.

## Automated prerequisite evidence

Run and retain evidence for:

```sh
make integration-test
make readiness-e2e
make backup-restore-readiness
make kubernetes-local-check
make kubernetes-local-verify
```

The integration gate exercises bucket creation, verified upload, digest
metadata, ranged reads, metadata-preserving self-copy, listing, and deletion
against a real RustFS container. It also performs a real MinIO-to-RustFS copy
with byte and metadata verification, then a RustFS-to-MinIO exact rollback that
removes a target-only key. Backup archives created after this migration are
RustFS-to-RustFS recovery artifacts and are not MinIO migration artifacts.

These automated gates validate RustFS and the application, but they do not copy
an operator's MinIO bucket. Production adoption additionally requires the
mandatory staged-cutover record from steps 1–10: inventories, initial and frozen
final `s3-migrate` JSON evidence, object and byte totals, manifest fingerprints,
canary results, and the named rollback owner. A release is not migration-ready
when only the automated prerequisites are green.
