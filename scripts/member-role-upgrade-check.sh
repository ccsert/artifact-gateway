#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
compose=(docker compose --env-file /dev/null --project-name artifact-gateway-integration -f "$root/compose.integration.yml")
probe_dir=$(mktemp -d)
probe_database=gateway_member_role_upgrade_test
rollback_database=gateway_member_role_rollback_upgrade_test
cleanup() {
  "${compose[@]}" exec -T postgres dropdb -U gateway --if-exists "$probe_database" >/dev/null 2>&1 || true
  "${compose[@]}" exec -T postgres dropdb -U gateway --if-exists "$rollback_database" >/dev/null 2>&1 || true
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
 ('00000000-0000-4000-8000-000000000014','pending-user','x','none'),
 ('00000000-0000-4000-8000-000000000015','unrecognized-level','x','owner');
INSERT INTO api_keys(id,name,secret_hash,roles) VALUES
 ('00000000-0000-4000-8000-000000000021','legacy-key','key-hash-1',ARRAY['writer']),
 ('00000000-0000-4000-8000-000000000022','mixed-key','key-hash-2',ARRAY['reader','admin']),
 ('00000000-0000-4000-8000-000000000023','level-less-key','key-hash-3',ARRAY[]::text[]);
SQL
# This migration replaces the reader and writer settings columns, so the
# documented rollback restores the pre-upgrade database instead of switching the
# image back. Keep that database and prove it restores as the schema the removed
# levels still accept.
"${compose[@]}" exec -T postgres pg_dump -U gateway -d "$probe_database" >"$probe_dir/before-upgrade.sql"
"${compose[@]}" run --rm --no-deps --env "PGDATABASE=$probe_database" migrate >"$probe_dir/upgrade.log" 2>&1 || { cat "$probe_dir/upgrade.log" >&2; exit 1; }
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
 IF (SELECT role FROM users WHERE name='legacy-reader')<>'member' THEN RAISE EXCEPTION 'legacy reader was not converted to member'; END IF;
 IF (SELECT role FROM users WHERE name='legacy-writer')<>'member' THEN RAISE EXCEPTION 'legacy writer was not converted to member'; END IF;
 IF (SELECT role FROM users WHERE name='platform-admin')<>'admin' THEN RAISE EXCEPTION 'administrator role changed'; END IF;
 IF (SELECT role FROM users WHERE name='pending-user')<>'none' THEN RAISE EXCEPTION 'pending role changed'; END IF;
 IF (SELECT role FROM users WHERE name='unrecognized-level')<>'member' THEN RAISE EXCEPTION 'unrecognized account level was not narrowed to member'; END IF;
 IF (SELECT roles FROM api_keys WHERE name='legacy-key')<>ARRAY['member']::text[] THEN RAISE EXCEPTION 'legacy api key roles were not narrowed to member'; END IF;
 IF (SELECT roles FROM api_keys WHERE name='mixed-key')<>ARRAY['admin']::text[] THEN RAISE EXCEPTION 'api key roles dropped a recognized level'; END IF;
 IF (SELECT roles FROM api_keys WHERE name='level-less-key')<>ARRAY[]::text[] THEN RAISE EXCEPTION 'a key with no level was given one'; END IF;
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
 BEGIN
   INSERT INTO users(id,name,secret_hash,role) VALUES ('00000000-0000-4000-8000-000000000031','rejected-reader','rejected-1','reader');
   RAISE EXCEPTION 'users.role still accepts the removed reader level';
 EXCEPTION WHEN check_violation THEN
   NULL;
 END;
 BEGIN
   INSERT INTO api_keys(id,name,secret_hash,roles) VALUES ('00000000-0000-4000-8000-000000000032','rejected-key','rejected-2',ARRAY['writer']);
   RAISE EXCEPTION 'api_keys.roles still accepts the removed writer level';
 EXCEPTION WHEN check_violation THEN
   NULL;
 END;
END $$;
SQL
printf 'Member role upgrade passed: legacy roles converted, grants materialized on active repositories only, grant-set versions untouched, removed levels narrowed out of accounts and API keys, and the database refuses them.\n'

# The migration report is the operator's inventory of the instances it cannot
# decide on its own. Seed one instance per class, plus one the report must not
# claim, and read the report back.
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$probe_database" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO resolver_audit_log(group_name, repository, actor, outcome, occurred_at, authorization_source, authorization_reason) VALUES
 ('upgrade-active','upgrade-active','legacy-pattern-reader','resolved',now(),'legacy_static','read_pattern_granted'),
 ('upgrade-active','upgrade-active','user:legacy-reader','resolved',now(),'legacy_static','read_pattern_granted');
INSERT INTO api_keys(id,name,secret_hash,roles) VALUES
 ('00000000-0000-4000-8000-000000000041','report-visible-key','report-hash-1',ARRAY['member']);
INSERT INTO service_accounts(id,name,state) VALUES
 ('00000000-0000-4000-8000-000000000042','report-disabled-account','disabled');
-- A grant needs its repository's grant-set row, which the migration created
-- for active repositories only.
INSERT INTO repository_grant_sets(repository_id, version) VALUES
 ('00000000-0000-4000-8000-000000000003',1);
INSERT INTO repository_grants(repository_id, principal, scopes, resource_prefix) VALUES
 ('00000000-0000-4000-8000-000000000001','api-key:00000000-0000-4000-8000-000000000041',ARRAY['repositories:read'],''),
 ('00000000-0000-4000-8000-000000000002','service-account:00000000-0000-4000-8000-000000000042',ARRAY['repositories:write'],''),
 ('00000000-0000-4000-8000-000000000003','user:legacy-reader',ARRAY['repositories:read'],''),
 ('00000000-0000-4000-8000-000000000001','user:ghost-account',ARRAY['repositories:read'],'');
SQL
report_database_url="postgres://gateway:integration-password@127.0.0.1:5432/$probe_database"
report_log="$probe_dir/report.log"
if "${compose[@]}" exec -T postgres env GATEWAY_DATABASE_URL="$report_database_url" sh -s <"$root/scripts/member-role-migration-report.sh" >"$report_log" 2>&1; then
  :
else
  cat "$report_log" >&2
  exit 1
fi
# The Makefile's docker stub answers no query, so this report is empty there and
# only its invocation is exercised. The content assertions need a real database
# and run in the contract job, which is where this script's other probes are
# effective too.
if [[ -s "$report_log" ]]; then
  report_section() { awk -v start="$1" -v end="$2" 'index($0, start) == 1, index($0, end) == 1' "$report_log"; }
  report_assert() {
    if [[ $2 == "tail" ]]; then
      report_tail=$(awk -v start="$1" 'index($0, start) == 1, 0' "$report_log")
      if ! grep -qF -- "$3" <<<"$report_tail"; then
        printf 'Migration report did not list %s in section %s:\n' "$3" "$1" >&2
        cat "$report_log" >&2
        exit 1
      fi
      return
    fi
    if ! report_section "$1" "$2" | grep -qF -- "$3"; then
      printf 'Migration report did not list %s in section %s:\n' "$3" "$1" >&2
      cat "$report_log" >&2
      exit 1
    fi
  }
  report_assert '2. Principals' '3. Grants' 'legacy-pattern-reader'
  report_assert '2. Principals' '3. Grants' 'upgrade-active'
  report_assert '3. Grants' '4a. Accounts' 'report-visible-key'
  report_assert '3. Grants' '4a. Accounts' 'report-disabled-account'
  report_assert '3. Grants' '4a. Accounts' '(disabled)'
  report_assert '4c. Grants' '4d. Grants' 'upgrade-deleted'
  report_assert '4d. Grants' tail 'user:ghost-account'
  if report_section '2. Principals' '3. Grants' | grep -qF -- 'user:legacy-reader'; then
    printf 'Migration report claimed an actor that already holds a grant:\n' >&2
    cat "$report_log" >&2
    exit 1
  fi
  for empty in '4a. Accounts' '4b. API keys'; do
    if ! report_section "$empty" '4c. Grants' | grep -qF '(none)'; then
      printf 'Migration report did not answer %s with (none):\n' "$empty" >&2
      cat "$report_log" >&2
      exit 1
    fi
  done
  printf 'Migration report listed every seeded instance of the four classes and nothing else.\n'
else
  printf 'Migration report ran without output; its content is asserted against a real database only.\n'
fi

rollback_database=gateway_member_role_rollback_upgrade_test
"${compose[@]}" exec -T postgres createdb -U gateway "$rollback_database"
"${compose[@]}" exec -T postgres psql -X -q -U gateway -d "$rollback_database" -v ON_ERROR_STOP=1 <"$probe_dir/before-upgrade.sql"
"${compose[@]}" exec -T postgres psql -X -U gateway -d "$rollback_database" -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
 IF (SELECT role FROM users WHERE name='legacy-reader')<>'reader' THEN RAISE EXCEPTION 'rollback database lost the reader level'; END IF;
 IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='oidc_settings' AND column_name='reader_roles') THEN RAISE EXCEPTION 'rollback database is not the pre-upgrade schema'; END IF;
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='users_role_check') THEN RAISE EXCEPTION 'rollback database already carries the converged level constraint'; END IF;
 IF (SELECT count(*) FROM hosted_repositories)<>3 THEN RAISE EXCEPTION 'rollback database lost its repositories'; END IF;
 IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='repository_grants') THEN RAISE EXCEPTION 'rollback database lost the repository grants table'; END IF;
END $$;
SQL
printf 'Member role rollback passed: the pre-upgrade database restores with the removed levels and without the converged constraints.\n'

