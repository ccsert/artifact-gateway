# Backup and Recovery Drill

Run this drill from a workstation with Docker Desktop, a configured `.env`, and
the local stack started by `make up`. The scripts keep backups under
`.artifacts/`, which is intentionally not part of source control.

## Targets

- RPO: the interval between successful runs of `scripts/backup-drill.sh`.
  The MVP drill target is 24 hours.
- RTO: 30 minutes from a declared recovery start until `/readyz` returns 204.

## Drill

1. Record the UTC start time and run `scripts/backup-drill.sh`.
2. Confirm the PostgreSQL dump and RustFS tar archive pass `shasum -a 256 --check <backup-dir>/SHA256SUMS`.
3. Make a reversible test change by creating a disposable Group or by fetching a
   Proxy artifact, then record the expected audit entry and cached object. For
   V2 validation, record the Raw canonical path or Conan revision coordinate,
   the member allowlist decision, and whether the read was authenticated or
   anonymous. When a managed Repository is in scope, record its grant-set ETag,
   a principal with `repositories:read`, and a separately authenticated
   principal without that scope; never record either credential.
4. Record the UTC recovery start time and run `scripts/restore-drill.sh <backup-dir>`.
5. Confirm `curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/readyz`
   returns `204`, query `GET /api/v1/audits` with an administrator token, and
   resolve the cached artifact. For V2 data, also resolve the recorded Raw path
   and Conan 2 revision through the restored Gateway and confirm their audit
   records retain format, actor, member, cache disposition, and outcome.
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
is valid only for the pinned RustFS baseline; use the
[S3 migration procedure](rustfs-migration.md) when moving from MinIO.
