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
2. Confirm the PostgreSQL dump and MinIO tar archive pass `shasum -a 256 --check <backup-dir>/SHA256SUMS`.
3. Make a reversible test change by creating a disposable Group or by fetching a
   Proxy artifact, then record the expected audit entry and cached object.
4. Record the UTC recovery start time and run `scripts/restore-drill.sh <backup-dir>`.
5. Confirm `curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/readyz`
   returns `204`, query `GET /api/v1/audits` with an administrator token, and
   resolve the cached artifact.
6. Record the UTC completion time, measured RTO, the backup timestamp used for
   RPO, and any failed verification in the incident record.

## Safety

`restore-drill.sh` overwrites the running PostgreSQL database and MinIO data.
Run it only against an isolated drill environment after preserving any data that
must be retained. It stops Gateway while the two stores are restored to avoid
new metadata pointing to objects from the interrupted state.
