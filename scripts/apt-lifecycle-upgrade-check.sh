#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
compose=(docker compose --env-file /dev/null --project-name artifact-gateway-integration -f "$root/compose.integration.yml")
probe_dir=$(mktemp -d)
probe_database=gateway_apt_lifecycle_upgrade_test
cleanup() {
  "${compose[@]}" exec -T postgres dropdb -U gateway --if-exists "$probe_database" >/dev/null 2>&1 || true
  rm -rf "$probe_dir"
}
trap cleanup EXIT
for source in "$root"/migrations/*.sql; do
  name=${source##*/}
  if [[ "$name" < 000117_native_apt_lifecycle.sql ]]; then cp "$source" "$probe_dir/$name"; fi
done
"${compose[@]}" exec -T postgres createdb -U gateway "$probe_database"
"${compose[@]}" run --rm --no-deps --env "PGDATABASE=$probe_database" --volume "$probe_dir:/migrations:ro" migrate >"$probe_dir/legacy.log" 2>&1 || { cat "$probe_dir/legacy.log" >&2; exit 1; }
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO hosted_repositories(id,name,format,repo_type) VALUES('00000000-0000-4000-8000-000000000001','apt-upgrade','apt','hosted');
INSERT INTO native_apt_repository_snapshots(id,repository_id,suite,sequence,state,created_at,published_at)
VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','stable',1,'retired',now()-interval '30 days',now()-interval '30 days'),
('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','stable',2,'visible',now(),now()),
('00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000001','stable',3,'building',now(),NULL);
SQL
"${compose[@]}" run --rm --no-deps --env "PGDATABASE=$probe_database" migrate >"$probe_dir/upgrade.log" 2>&1 || { cat "$probe_dir/upgrade.log" >&2; exit 1; }
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
 IF (SELECT count(*) FROM native_apt_repository_snapshots)<>3 THEN RAISE EXCEPTION 'upgrade lost legacy snapshots'; END IF;
 IF NOT EXISTS(SELECT 1 FROM native_apt_repository_snapshots WHERE state='retired' AND retired_at>now()-interval '5 minutes') THEN RAISE EXCEPTION 'legacy retirement did not receive a fresh grace period'; END IF;
 IF EXISTS(SELECT 1 FROM native_apt_repository_snapshots WHERE state<>'retired' AND retired_at IS NOT NULL) THEN RAISE EXCEPTION 'upgrade changed active snapshot retirement'; END IF;
END $$;
INSERT INTO native_apt_snapshot_assets(snapshot_id,repository_id,path,digest,object_key,size,content_type)
VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','dists/stable/main/binary-amd64/Packages','sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855','native/apt/sha256/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',0,'text/plain');
SQL
sed -n '/^-- +goose Down/,$p' "$root/migrations/000117_native_apt_lifecycle.sql" >"$probe_dir/down.sql"
if "${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <"$probe_dir/down.sql" >"$probe_dir/down.log" 2>&1; then
  printf 'Unsafe APT lifecycle downgrade unexpectedly succeeded.\n' >&2; exit 1
fi
grep -Fq 'APT lifecycle requires backup restoration for downgrade' "$probe_dir/down.log"
printf 'APT lifecycle upgrade passed: legacy rows preserved, fresh retirement grace, empty index constraint, and unsafe downgrade rejected.\n'
