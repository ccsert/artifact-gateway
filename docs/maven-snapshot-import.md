# Historical Maven SNAPSHOT import

[简体中文](maven-snapshot-import.zh-CN.md) | [Documentation index](README.md)

`gateway snapshot-import` is an **Unreleased** operator command for a frozen,
offline Maven SNAPSHOT bundle. Published v0.6.1 does not contain it. It preserves
admitted source timestamp/build filenames, extension/classifier pairs, complete
primary bytes and version metadata bytes. Each metadata pair keeps its source
selection, including an older build or different builds for different pairs.
Normal Maven PUT/session publication continues to allocate a new local identity
and must not be used to preserve historical identities.

This implements the scoped import in [#257](https://github.com/ccsert/artifact-gateway/issues/257)
under [#199](https://github.com/ccsert/artifact-gateway/issues/199).
Read-only client verification [#204](https://github.com/ccsert/artifact-gateway/issues/204)
and the broader migration ledger [#205](https://github.com/ccsert/artifact-gateway/issues/205)
remain separate. This command does not fetch upstream artifacts, establish
complete dependency closure, create a Repository, discover credentials, start
services or change production configuration.

## Admission contract

- Supply a fixed `manifest.json` SHA-256 and an opaque source ID. `complete:true`
  is an operator attestation that the frozen source inventory is complete;
  source acquisition and that completeness evidence remain external. Every
  listed primary file and metadata document is streamed and checked in full.
- A GAV must end in `-SNAPSHOT`. A build is identified by **timestamp plus build
  number**, not build number alone. Duplicate numbers at different timestamps
  and different numbers at the same timestamp are retained. Paths, POM GAV
  (including literal inherited parent group/version), packaging and required
  main artifacts must agree. Supported packaging is `jar`, `maven-plugin`,
  `ejb`, `bundle`, `war`, `ear`, `rar`, `zip`, and `pom`. Custom packaging or
  unresolved POM expressions are rejected, without repair.
- Version metadata must explicitly select existing admitted timestamp/build
  values through unique extension/classifier `snapshotVersions`. Maven also
  supports older metadata containing only a global timestamp/build; **v1 does
  not support that representation**, and reports it as unsupported. A missing
  `metadata` field is rejected. Explicit `metadata:null` means the source has
  no current version metadata: historical URLs work, while version metadata
  and canonical `-SNAPSHOT` aliases return 404.
- Missing main files, inconsistent POMs/paths, unknown fields, duplicate JSON
  fields, duplicate XML identity fields, symlinks, unsafe paths, changed bytes,
  and invalid current references reject the entire bundle before target writes.
  The report lists rejected GAVs. To defer anomalous assets, explicitly exclude
  their **whole GAV** with a reason in a reviewed manifest *before apply*.
  Good builds in that same GAV are also deferred. Exclusion is an inventory
  decision, not an automatic native security-quarantine record. No assets,
  POMs, metadata selections or missing main files are synthesized to repair it.
- SHA-256 identities cover primary files, signatures and raw version metadata.
  Source checksum sidecars are not manifest primary files. The command derives
  lowercase MD5/SHA-1/SHA-256/SHA-512 sidecars from verified bytes; their text
  serialization is not claimed to preserve source sidecar whitespace/case.
- The target must be an existing active Maven Hosted Repository with retention
  **disabled**. Preflight, reservation and commit recheck this under the same
  Repository lock as retention updates. The command does not alter retention.
  Source timestamps become artifact `createdAt`; checkpoint time records the
  actual import. Re-enabling age/count retention after acceptance can delete
  histories and their selected current build, and is a separate operator choice.

The manifest is strict version-1 JSON, at most 8 MiB. POMs are at most 16 MiB,
metadata at most 4 MiB, individual primary files at most 1 TiB. Declared integer
sizes and SHA-256 digests are mandatory, including for zero-byte files. At least
one admitted GAV is required. Operator hosts are Linux/macOS. Each primary
upload uses an anonymous private temporary spool, hashed in the same copy before
any object write; reserve local temporary space for the largest primary plus
headroom separately from target storage capacity. The following is a **template**: replace sizes and
hash placeholders with measured evidence and list every admitted build/file.

```json
{
  "schemaVersion": 1,
  "sourceId": "frozen-source-20261006",
  "complete": true,
  "coordinates": [{
    "coordinate": "org.example:widget:1.0-SNAPSHOT",
    "builds": [{
      "timestamp": "20260101.000000",
      "buildNumber": 7,
      "files": [
        {"path": "org/example/widget/1.0-SNAPSHOT/widget-1.0-20260101.000000-7.pom", "digest": "sha256:<64 lowercase hex>", "size": 123},
        {"path": "org/example/widget/1.0-SNAPSHOT/widget-1.0-20260101.000000-7.jar", "digest": "sha256:<64 lowercase hex>", "size": 456}
      ]
    }],
    "metadata": {"path": "org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", "digest": "sha256:<64 lowercase hex>", "size": 789}
  }],
  "excluded": [{"coordinate": "org.example:broken:1.0-SNAPSHOT", "reason": "missing-main-artifact"}]
}
```

Place files at those exact paths relative to the bundle directory. Freeze the
bundle against modification. Verify full source byte coverage; a previously
sampled subset is not complete import evidence. Keep acquisition URLs, private
coordinates, raw assets, credentials and operational evidence outside public
issues, PRs and the Git repository.

## Operator sequence

1. Provision and review the explicit source-to-target Repository mapping through
   existing administration. Use an isolated target for rehearsal. Before real
   apply, complete the [recovery runbook](recovery-runbook.md) with a validated,
   paired PostgreSQL/S3 backup and the complete writer scope. All Gateway API,
   reader, scheduler and worker processes connected to the target must run the
   same revision containing this importer and migration `000141`; apply the
   migration through the existing migration workflow. **Do not import into a
   mixed old/new revision deployment.** An old binary does not honor import
   reservations or the preserved selectors. Schema addition alone is insufficient.
2. Keep target retention disabled through acceptance. Use an existing privileged
   operator's database/S3 credential references; this command does not grant
   permissions. Database identity uses `pg_control_system()` plus database name;
   insufficient permission rejects without requesting or granting access.
3. Pin the exact manifest hash and run source-only verification. `verify` loads
   no target configuration and contacts no source or target:

   ```sh
   umask 077
   manifest_digest="sha256:$(shasum -a 256 /private/bundle/manifest.json | awk '{print $1}')"
   /path/to/import-capable/gateway snapshot-import verify \
     --bundle /private/bundle --manifest-sha256 "$manifest_digest" > /private/verify.json
   ```

4. Write an explicit regular target spec with mode `0600`. It contains existing
   environment variable *names*, not secret values. No runtime `.env`, Kubernetes
   or Jenkins credential discovery is performed. Example template:

   ```json
   {
     "targetId": "reviewed-snapshot-target",
     "repositoryId": "11111111-1111-4111-8111-111111111111",
     "actor": "migration-operator",
     "databaseUrlEnv": "MIGRATION_DATABASE_URL",
     "s3Endpoint": "https://s3.example.invalid",
     "s3Bucket": "reviewed-artifact-bucket",
     "s3AccessKeyEnv": "MIGRATION_S3_ACCESS_KEY",
     "s3SecretKeyEnv": "MIGRATION_S3_SECRET_KEY"
   }
   ```

   The endpoint accepts HTTP/HTTPS without userinfo, query, fragment or a path.
   The actor is an accountable operator label, not a replacement for database/S3
   authorization. Acquire existing values through the approved operator process.

5. Run target dry-run. It checks Repository/checkpoint conflicts, existing
   physical object bytes and bucket availability without creating objects,
   sessions, checkpoints, metadata or buckets:

   ```sh
   /path/to/import-capable/gateway snapshot-import dry-run \
     --bundle /private/bundle --manifest-sha256 "$manifest_digest" \
     --spec /private/target.json > /private/dry-run.json
   ```

   Its `targetBinding` hashes the actual PostgreSQL cluster/database and normalized
   S3 endpoint/bucket. Changing the physical target is rejected even if `targetId`
   stays the same; rotating credentials for the same target is allowed.
6. Build a fresh private [capacity plan](migration-capacity-preflight.md) from
   real logical usage/quota, object/reference membership, physical storage pool
   free space, temporary upload/download, backup/restore and headroom evidence.
   Use `inventoryId = manifestDigest`, and both plan/snapshot `targetId =
   dry-run.targetBinding`. Copy the exact `capacityReferences` set from dry-run,
   including derived checksum and metadata objects, sizes and repository IDs.
   A missing, stale, insufficient, mismatched or unknown plan rejects apply.
   Freshness is checked again after lock waiting, before each GAV reservation.
   Unknown physical capacity must remain unknown. Evaluate it read-only:

   ```sh
   /path/to/import-capable/gateway preflight capacity --input /private/capacity.json --format json
   ```

7. Apply the identical frozen bundle with the bound sufficient capacity plan:

   ```sh
   /path/to/import-capable/gateway snapshot-import apply \
     --bundle /private/bundle --manifest-sha256 "$manifest_digest" \
     --spec /private/target.json --capacity-plan /private/capacity.json > /private/apply.json
   ```

   Preserve the manifest/hash, private spec, source completeness evidence,
   capacity evidence and every JSON report. Verify historical and source-selected
   current HTTP GET/HEAD URLs and clients in the reviewed target scope before
   cutover. This change's fixtures are synthetic; they do not certify a private
   source inventory, a production target or full dependency closure.

## Commit, recovery and evidence

PostgreSQL checkpoints are durable in `native_maven_snapshot_imports`, bound to
source ID, manifest SHA-256, Repository, target ID/binding and complete per-GAV
plan. The native publish session, object intents, references, quota trigger and
lifecycle collector are reused. Importers serialize per GAV; importer I/O and
GC deletion share an object lock. Live uploads protect deduplicated objects even
when another session originally created their intent. Active GC claims reject
recovery; expired claims are reset under that lock, and stale GC jobs cannot
delete recovered bytes.

A GAV remains invisible until all of its primary/derived bytes are uploaded or
fully verified, then all artifacts/assets, exact metadata selector, session and
one `maven.snapshot.import` audit event commit atomically. Existing GAVs, even
tombstoned history, sessions, paths or different checkpoints reject; there is no
force/overwrite option. Imported GAVs remain reserved against ordinary Maven
PUT/session publication (`409 snapshot_import_reserved`). Artifact lists/search
expose `sourceTimestamp`/`sourceBuildNumber`; existing `buildNumber` remains a
unique **local publication sequence** for cursors/browse, not the source number.

Failure before commit exposes no partial GAV. Earlier committed GAVs retain
history and resolution. Replay the **same** source/manifest/target with fresh
capacity evidence; staged sessions renew for 24 hours, missing bytes may be
uploaded again, and committed GAVs are verified without republishing. A lost
commit response is resolved from the checkpoint and an identical retry. Never
edit the manifest, delete checkpoints or republish to bypass a conflict. A
post-import downgrade to an old binary requires the validated paired pre-import
restore; deleting schema columns/checkpoints is not a supported rollback.

Reports have schema version 1, `checkedAt`, fixed identities, separate
`rejected`/`excluded` lists, per-GAV session/state/reason and counts. `verify`
returns `source-verified`; dry-run returns `ready`; apply returns `verified` only
after full target bytes and visible primary paths match. `partial` retains
completed and failed/pending states; a confirmed commit with failed readback
remains `committed`, not `verified`. Counts describe exclusive current states,
not generic migration ledger completion. Exit 0 means that action succeeded,
1 means operational failure/rejection, and 2 means argument/spec/report failure.
Do not ignore the exit code when saving stdout.

The audit is searchable by target Repository name; evidence records Repository
ID, manifest/source/session and target binding. Retention/tombstone or configured
security quarantine can hide imported builds. A hidden selected build does not
switch metadata to a sibling: unavailable current metadata returns 404, and
quarantined artifact GET/HEAD follows existing denial policy, including Groups.

## Reproducible checks

The public synthetic fixture covers three histories, repeated source build
numbers, classifier/extension pairs and old/mixed current selections. Regressions
also cover same timestamp builds 1/10/70 with identical POMs, missing files,
metadata/POM mismatch, explicit absence, whole-GAV exclusion, source/target byte
drift, multiple-GAV failures, concurrent replay, lost commit response, retention
admission, ordinary same-actor PUT, quarantine and real PostgreSQL/RustFS CLI
replay/expired-claim recovery.

```sh
go test ./internal/snapshotimport ./internal/repository ./internal/protocol/maven ./internal/app ./cmd/gateway
make integration-test
make native-maven-e2e
make ci-local-full
```

These gates use isolated local fixtures. No production import or release is
performed by this implementation work.
