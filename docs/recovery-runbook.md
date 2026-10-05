# Backup and Recovery Drill

[简体中文](recovery-runbook.zh-CN.md)

## Portable offline S3 byte transfers (Unreleased)

`gateway backup export` and `gateway backup restore` implement the #200/#201
offline transfer profile independently of agctl. They use explicit private JSON
specs and never load the server `.env`. Export reads a declared, already stopped
source; it does not stop or restart writers. The operator must inventory every
API, worker, and external writer, stop them before export, and keep them stopped
until the command finishes. Observed stop identities and repeated DB metadata,
inventory, reference closure, and full-byte checks detect changes; they are not
a distributed fence. Consistency remains an operator responsibility.

```sh
gateway backup export --spec /private/source.json --bundle /private/new-bundle
gateway backup restore --spec /private/new-target.json --bundle /private/new-bundle
```

Specs must be private regular JSON files (0600, at most 1 MiB), with no duplicate
or unknown fields. The new bundle root is 0700 and files are 0600. Export refuses
an existing directory. Failures leave `INCOMPLETE` and local evidence; only a
finished transfer publishes `manifest.json` and removes `INCOMPLETE`. Keep the
bundle immutable and operator controlled throughout verification and restore.
Do not use an untrusted dump or executable: hashes establish identity, not trust.

Failed exports preserve a private `failure.json` (0600) with backup ID, UTC time,
stage, current object key/relative file and a safe reason code; keep it inside
the controlled bundle. Raw upstream errors are discarded.

The source spec contains these explicit fields:

| Field | Contract |
| --- | --- |
| `backupId`, `scopeId`, `inventoryDeclaredComplete` | Nonsecret opaque IDs and an explicit complete-writer declaration. |
| `writers` | Nonempty array of `{id, kind, reference}`. `docker-container` requires the full 64-digit container ID; `systemd-unit` requires an explicit `.service` unit. Each must be inactive/stopped with the same observed stop identity before and after transfer. |
| `docker.context` | Named local Unix-socket context for Docker writers, OCI images or container PG tools. Remote Docker is refused; inherited `DOCKER_*`/`COMPOSE_*` cannot redirect subprocesses. |
| `postgres` | `dsn` is an explicit `postgres://user:password@127.0.0.1:port/database?sslmode=disable` URL (`localhost` also accepted). No service files, defaults or additional URL options. Optional `toolsContainer` must identify the running PG container with that exact loopback binding; otherwise local `pg_dump` is used. |
| `s3` | Explicit `endpoint`, `bucket`, `accessKey`, `secretKey`. Export uses S3 LIST/GET, never physical RustFS paths or multipart ETags as byte digests. |
| `release` | Operator-approved local `directory`, `identity` and, for OCI, `imageReference`, as described below. |

Release `directory` contains the exact `migrations/*.sql` files for the recorded
applied ledger. Binary releases also contain `gateway`; identity uses
`{version, revision, artifact:{kind:"binary", sha256:"sha256:...", platform:"linux/arm64"}}`
(or `linux/amd64`). Full executable bytes, Go module/platform and every migration
checksum must agree. Without `nativeArchive`, static injected version/revision
must also agree, retaining strict legacy behavior. OCI uses
`kind:"oci-image"`, the approved manifest digest in `artifact.sha256`, and a
digest-pinned `imageReference`; the image must already be local, its RepoDigest,
platform and OCI version/revision labels must agree. Bundle contents never
choose a downloadable executable or image. Version 1 retains its image-only
`imageDigest`; new exports produce version 2 with unambiguous artifact identity.

Published native trimpath builds can omit injected identity from Go BuildInfo.
Archive fallback requires the entire `-ldflags` setting to be absent. When it is
present, strict parsed version/revision comparison remains mandatory, including
rejecting quoted assignments the legacy parser cannot interpret.
For these binaries, source and restore specs may add
`release.nativeArchive: {"path":"/private/original-release.tar.gz","sha256":"sha256:<64 lowercase hex digits>"}`.
This optional field is outside the backup manifest and is invalid for OCI.
Older tools reject it as unknown; use a tool containing the fix, keeping the
approved original source/restored executables unchanged. It does not rewrite
v0.5.0/v0.6.0 assets or decide the next tool/release version.

The trust root is the operator-controlled private spec **outside the bundle**.
Before approving it, independently resolve the official GitHub tag to its full
commit, check the version, obtain the release API's full archive and SHA256SUMS
digests over HTTPS, and verify both downloaded files and the archive checksum
line. Pin these values; a bundle, arbitrary archive's own checksum, or detached
VERSION.txt cannot establish approval. Verification itself is offline. Digests
bind approved bytes; they are not signed attestations. Publisher/GitHub or
approved-spec compromise is outside this boundary. Upstream replacement fails
the approved pins; a deliberately approved old archive works only with its
matching version/revision/platform/ledger, never as another release. Keep the
archive and release directory immutable and operator controlled throughout
verification/restore; hostile concurrent mutation exceeds the existing local
filesystem guarantee.

The verifier hashes the full absolute-path regular archive, then streams its
original VERSION.txt, gateway and all migrations without extraction/execution.
VERSION's exact version/full revision/platform must match the trusted spec;
gateway and every SQL digest must match the actual directory and complete DB
ledger. Present static injection cannot conflict. Rehashing tampered binary/SQL
does not override the separately pinned archive. Traversal/absolute/alias paths,
wrapper roots, soft/hard links, duplicate names, unknown entries, multiple gzip
members and corrupt/truncated compression fail. Caps: 512 MiB compressed,
1 GiB decoded including padding, 256 MiB per regular entry, 4 MiB per SQL,
4 KiB VERSION, 4096 entries.

`bash scripts/published-native-backup-test.sh` downloads fixed official v0.5.0/
v0.6.0 linux/amd64 original bytes for static and tamper/replay regressions.
`make published-native-backup-test` additionally runs the existing owned random
PG/RustFS fixture: export/verify/restore, Raw Group/grant reads, source/sentinel
preservation and negative restores. Original binaries run only after static
trust verification; runtime self-report is not a trust source. CI runs this
at the PR SHA. Every future packaged Linux distribution also passes the same
static verifier with actual trimpath parameters. A static pass cannot replace
completed PG/RustFS or production acceptance.

The exporter streams the entire sorted bucket, including unreferenced bytes,
and then repeats LIST/GET and verifies published DB references are present.
Intent/GC rows do not imply published bytes. A wholly absent Cargo schema is
supported for pre-Cargo v0.4.2 reference queries; a partially present Cargo
schema is rejected. Release and complete migration-ledger matching still apply.
This does not prove a v0.4.2-to-current upgrade: forward migration is a separate
acceptance step, and downward migration is unsupported.

The restore spec contains `project`, `docker`, `release`, `postgresPassword`,
`accessKey`, `secretKey`, `rpcSecret`, `adminToken` and `resolverToken`. Supply
temporary drill values yourself; the command does not mint credentials or
change IAM/grants. `project` must be a fresh `ag-restore-...` name, at most 42
characters. Existing networks, volumes or service containers are refused even
if empty. No arbitrary target DSN, endpoint, bucket or Compose `.env` is accepted.
The adapter creates a dedicated local bridge network, two fresh local volumes,
new PostgreSQL 16 and pinned RustFS services, and a new Gateway. Every published
port binds only 127.0.0.1; identity, ownership labels, mounts and sole network
membership are checked before writes. Bridge isolation separates data/projects;
it is not an outbound network firewall. Gateway runs with API roles, a read-only
root, dropped capabilities and a bounded `/tmp` memory mount for protocol spools.

All input bytes, approved software/schema and the complete decompressed PG custom
archive are checked before target creation. `pg_restore --exit-on-error` then
restores the new DB; all public-table fingerprints (including grant sets,
Group membership, historical audits and durable work) and the full ledger are
compared **before Gateway startup**. S3 objects are uploaded and fully read back.
Readiness, protocol and authorization remain separate report fields.

Optional `readerToken`, `deniedToken` and `readChecks` enable fixed Raw/OCI GET
assertions. Each check has `kind` (`protocol`, `grant-allow`, `grant-deny`),
`format` (`raw`, `oci`), relative protocol `path`, `credential` (`admin`, `none`,
`reader`, `denied`) and expected `status`. A successful read requires `size` and
`sha256`; grant checks also require the expected `principal` actor returned by
`/api/v2/identity`. Denials accept 401/403 and require a separately authenticated
denied actor. For example, include an allow and deny check for the same Group
path, and record both source digests and ordered membership independently.
Only an allow **and** a deny set `authorization: verified`; no checks leave it
`not_run`. This is scoped evidence for those checks, not every protocol/identity.

`runtimeEnvironment` permits only existing reader/legacy-read, settings-encryption,
egress-key and OIDC settings needed to interpret restored data. Preserve required
cryptographic/runtime settings through controlled specs; never put their values
in public reports. Endpoint, DB, S3, role and instance settings cannot be overridden.

Transfers exit 0 for `exported`/`restored`, 1 for a failed transfer, and 2 for
invalid invocation/spec input or report output. Reports contain backup ID, UTC
check time, software identity, schema digest, byte counts and separate database,
metadata, grant/Group/audit metadata, objects, readiness, protocol and
authorization results. Metadata results describe exact pre-start fingerprints;
new readback audits after startup are expected. Source coordinates, keys, raw
errors and credentials stay out of stdout. Counts on failure are a verified
prefix. `preflight backup` still reports overall consistency unknown and exit 3;
its success at hashing is never real recovery acceptance.

Successful targets remain available for controlled review. Record their fresh
project and inspect their owner labels and exact container/network IDs before
any later cleanup. Failure cleanup removes only IDs/names created by that
invocation with matching ownership, mount and network evidence; a changed or
foreign resource is retained and reported as incomplete cleanup. Cleanup never
uses project-wide `compose down`, source bucket deletion or existing volumes.
SIGINT/SIGTERM cancel the transfer and use a bounded independent cleanup context.
For a retry, choose a fresh project; this entry point does not adopt or overwrite
an existing target. It does not reopen traffic, deploy, release or merge.

Run `make backup-transfer-test` for real synthetic PG/S3 recovery through the
public export/restore commands, separately for binary and OCI software profiles.
Both use Raw Hosted/Group data: three objects / 54 bytes include an unreferenced
object; the whole bucket, current and pre-Cargo reference queries, post-backup
mutation exclusion, grant allow/deny, Group order, historical audits and protected
source/sentinel identities/data/health are checked. Eleven rejection/startup
failure scenarios per profile verify only invocation-owned target cleanup.

The OCI fixture builds the checkout binary into an image with the existing
digest-pinned runtime. It pushes anonymously to a randomly named disposable
registry pinned at `registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373`,
bound only to `127.0.0.1` inside the validated local daemon (including Docker
Desktop's VM), with a private tmpfs and no adopted network or volume. A temporary
Docker configuration explicitly declares anonymous registries; rejecting helper
traps prove host credential helpers are unused. Restore uses the actual pushed
digest and the host's supported Linux platform. The suite checks
actual RepoDigest, platform, labels, running image ID, lack of binary mounts and
public Gateway build diagnostics. Wrong digest/version/revision/platform, a
mutable tag, an image ID alone and an unavailable local image fail public export
and restore without publishing a bundle or creating a target. The fixture image
and registry are removed only after exact captured ID, name, label and binding
checks; altered resources are retained and fail cleanup. Shared pinned base image
and build caches remain. No persistent credential or daemon setting is created,
and no external Gateway package visibility or permissions are assumed.

OCI here describes the **Gateway software profile**, not OCI artifact protocol
recovery. Raw is the only protocol exercised in this transfer fixture. The OCI
and binary profiles both build the current checkout.
`make backup-restore-readiness` remains the separate physical profile gate.
These suites do not prove production fencing, every format or identity provider,
systemd controllers, cross-version upgrade, a large-object interruption matrix,
or recovery from a cloud-specific S3 implementation. #200/#201 remain broader
acceptance work; neither preflight nor these scoped tests close that matrix.

## Alert mail recovery gate and safe upgrade rollback (v0.6.0 preparation)

The full PostgreSQL dump and dynamic public-table fingerprints already include
new mail/quota tables; there is no table allowlist to update. The release gate
adds nonempty encrypted targets, test request records, quota state/episodes/events
and descriptors, and pending/retrying, active/expired, cancelled and terminal
deliveries to both existing binary/OCI recovery profiles. Eight concurrent
opted-in workers use an owned, non-forwarding TLS SMTP sink: only permitted due
work sends, with critical blocked behind the same episode’s in-flight warning. Cancelled expired leases become dead with possibleDuplicate; accepted
and dead rows stay terminal. Active leases and future retry deadlines block work,
expired unrevoked attempts retain possibleDuplicate, and stale tokens cannot
finish a newer claim. Safe API reads and administrator denials are also checked.

`GATEWAY_SETTINGS_ENCRYPTION_KEY` is an independent operational prerequisite.
Back up/escrow it separately under the operator's secret-management policy; the
bundle stores encrypted recipient snapshots, never the encryption key, SMTP
authentication files, or runtime configuration. Supply the original key through
the private target runtime settings. Missing/wrong keys cannot decrypt. Restored
Gateway is API-only and cannot enable SMTP through the runtime allowlist. Before
explicitly starting any mail worker, review pending/in-flight/possibleDuplicate
and cancellation state. Restoring an earlier snapshot can repeat an externally
accepted attempt; SMTP is not exactly-once and accepted mail cannot be recalled.

`make upgrade-readiness` pins formal v0.5.0 `ea60aea333b29bb60d4ac1b8e2b2a8720563726f`.
Stop all API/Scheduler/Worker and external writers; retain a consistent pre-upgrade
PG + object snapshot and matching software/config/key. Apply 000136–139 with one
migration job before starting same-revision roles and Console; default mail stays
disabled. The gate checks replay no-op, core identity/bytes and new-state readback,
then restores that pre-upgrade snapshot into a new v0.5.0 target and rolls forward.
Objects use the same S3 byte format; DB changes are additive forward migrations.
Additive schema alone does not approve running an old binary against the expanded
DB. No down migration or intermediate pre-000139 mail worker is approved here.
Full snapshot rollback loses changes after the snapshot and requires reviewing
external mail side effects before traffic/worker enablement. Published-registry
and production deployment acceptance, provider/protocol/scale matrices remain open.

The physical `restore-drill.sh` restarts its original Gateway container. If an
operator customized it with mail roles/configuration, disable/drain mail first
and explicitly review/authorize re-enabling it after recovery.

## Pinned physical drill

Run this drill from a workstation with Docker Desktop, a configured `.env`, and
the local stack started by `make up`. The scripts keep backups under
`.artifacts/`, which is intentionally not part of source control.

## Targets

The Unreleased [offline manifest validator](backup-manifest-verification.md)
verifies private local bytes and declarations only. Real portable transfers use
the separate commands above; neither validator nor transfer replaces this pinned
physical drill. Validator unknown results must not be treated as backup success.

- RPO: the interval between successful runs of `scripts/backup-drill.sh`.
  The MVP drill target is 24 hours.
- RTO: 30 minutes from a declared recovery start until `/readyz` returns 204.

## Drill

1. Record the UTC start time and establish the evidence that the backup must
   preserve: create or fetch a test Artifact, record its expected audit entry,
   and resolve it through its protocol client. For V2 validation, record the
   Raw canonical path or Conan revision coordinate, the member allowlist
   decision, and whether the read was authenticated or anonymous. For Go Hosted
   validation, record the module path, version, and digests of its `.info`,
   `.mod`, and `.zip` representations, then resolve it with a fresh
   `GOMODCACHE`. When a managed Repository is in scope, record its grant-set ETag,
   a principal with `repositories:read`, and a separately authenticated
   principal without that scope; never record either credential.
2. Run `scripts/backup-drill.sh`, then confirm the PostgreSQL dump and RustFS
   tar archive pass `shasum -a 256 --check <backup-dir>/SHA256SUMS`.
3. Make a reversible post-backup mutation by creating a disposable Group or an
   additional Artifact version. Record the resource and, when practical, its
   unique object digest; both its metadata and unreferenced object must be
   absent after restore.
4. Record the UTC recovery start time and run `scripts/restore-drill.sh <backup-dir>`.
5. Confirm `curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/readyz`
   returns `204`, query `GET /api/v1/audits` with an administrator token, and
   resolve the cached artifact. For V2 data, also resolve the recorded Raw path
   and Conan 2 revision through the restored Gateway and confirm their audit
   records retain format, actor, member, cache disposition, and outcome. For Go
   Hosted data, repeat the download with another fresh `GOMODCACHE` and verify
   all three representation digests still match the recorded values.
   Confirm the post-backup mutation is absent through both its management or
   protocol API and a direct object-store lookup of its unique object digest.
   For a managed Repository, confirm the recorded grant-set ETag and principal
   remain present, the granted principal reads the recorded object, and the
   ungranted principal receives the protocol's normal authorization denial.
   Treat an unexpected allow as a security incident: keep the Repository out of
   service, preserve the backup and audit evidence, and restore the last known
   grant set using an administrator before reopening traffic.
6. Record the UTC completion time, measured RTO, the backup timestamp used for
   RPO, and any failed verification in the incident record.

## Safety

`restore-drill.sh` overwrites the running PostgreSQL database and RustFS data.
Run it only against an isolated drill environment after preserving any data that
must be retained. It stops Gateway while the two stores are restored to avoid
new metadata pointing to objects from the interrupted state. The object archive
is valid only for the pinned RustFS baseline. The project no longer ships or
supports a legacy object-store migration path.
