#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
compose=(docker compose --env-file /dev/null --project-name artifact-gateway-integration -f "$root/compose.integration.yml")
probe_dir=$(mktemp -d)
probe_database=gateway_member_role_upgrade_test
cleanup() {
  "${compose[@]}" exec -T postgres dropdb -U gateway --if-exists "$probe_database" >/dev/null 2>&1 || true
  rm -rf "$probe_dir"
}
trap cleanup EXIT
for source in "$root"/migrations/*.sql; do
  name=${source##*/}
  if [[ "$name" < 000123_member_level_and_repository_grants.sql ]]; then cp "$source" "$probe_dir/$name"; fi
done
"${compose[@]}" exec -T postgres createdb -U gateway "$probe_database"
"${compose[@]}" run --rm --no-deps --env "PGDATABASE=$probe_database" --volume "$probe_dir:/migrations:ro" migrate >"$probe_dir/legacy.log" 2>&1 || { cat "$probe_dir/legacy.log" >&2; exit 1; }
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO hosted_repositories(id,name,format,repo_type,state) VALUES
 ('00000000-0000-4000-8000-000000000001','upgrade-active','raw','hosted','active'),
 ('00000000-0000-4000-8000-000000000002','upgrade-second','raw','hosted','active'),
 ('00000000-0000-4000-8000-000000000003','upgrade-deleted','raw','hosted','deleted');
INSERT INTO users(id,name,secret_hash,role) VALUES
 ('00000000-0000-4000-8000-000000000011','legacy-reader','x','reader'),
 ('00000000-0000-4000-8000-000000000012','legacy-writer','x','writer'),
 ('00000000-0000-4000-8000-000000000013','platform-admin','x','admin'),
 ('00000000-0000-4000-8000-000000000014','pending-user','x','none');
SQL
"${compose[@]}" run --rm --no-deps --env "PGDATABASE=$probe_database" migrate >"$probe_dir/upgrade.log" 2>&1 || { cat "$probe_dir/upgrade.log" >&2; exit 1; }
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
 IF (SELECT role FROM users WHERE name='legacy-reader')<>'member' THEN RAISE EXCEPTION 'legacy reader was not converted to member'; END IF;
 IF (SELECT role FROM users WHERE name='legacy-writer')<>'member' THEN RAISE EXCEPTION 'legacy writer was not converted to member'; END IF;
 IF (SELECT role FROM users WHERE name='platform-admin')<>'admin' THEN RAISE EXCEPTION 'administrator role changed'; END IF;
 IF (SELECT role FROM users WHERE name='pending-user')<>'none' THEN RAISE EXCEPTION 'pending role changed'; END IF;
 IF (SELECT count(*) FROM repository_grants WHERE principal='user:legacy-reader' AND scopes=ARRAY['repositories:read']::text[])<>2 THEN RAISE EXCEPTION 'reader grants were not materialized on active repositories'; END IF;
 IF (SELECT count(*) FROM repository_grants WHERE principal='user:legacy-writer' AND scopes=ARRAY['repositories:write']::text[])<>2 THEN RAISE EXCEPTION 'writer grants were not materialized on active repositories'; END IF;
 IF EXISTS (SELECT 1 FROM repository_grants WHERE principal IN ('user:platform-admin','user:pending-user')) THEN RAISE EXCEPTION 'grants were materialized for accounts that had none'; END IF;
 IF EXISTS (SELECT 1 FROM repository_grants g JOIN hosted_repositories r ON r.id=g.repository_id WHERE r.state<>'active') THEN RAISE EXCEPTION 'grants reached a non-active repository'; END IF;
 IF EXISTS (SELECT 1 FROM repository_grant_sets WHERE version<>1) THEN RAISE EXCEPTION 'materialized grants marked a grant set managed'; END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='oidc_settings_jit_default_role_check' AND pg_get_constraintdef(oid) LIKE '%member%') THEN RAISE EXCEPTION 'jit default role constraint does not accept member'; END IF;
 IF EXISTS (SELECT 1 FROM oidc_settings) THEN
   UPDATE oidc_settings SET jit_default_role='member';
   IF NOT EXISTS (SELECT 1 FROM oidc_settings WHERE jit_default_role='member') THEN RAISE EXCEPTION 'member was rejected as a jit default role'; END IF;
 END IF;
END $$;
SQL
printf 'Member role upgrade passed: legacy roles converted, grants materialized on active repositories only, grant-set versions untouched, and member accepted as a JIT default role.\n'
