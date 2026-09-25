#!/usr/bin/env bash
# Read-only inventory of the instances the reader/writer level migration cannot
# decide on its own. It queries the database and writes nothing, so it is safe
# on a live deployment, before or after the upgrade: the account-level and
# key-level sections list what the upgrade is about to collapse, and the grant
# sections list what it left behind.
#
# The upgrade check pipes this file into the postgres image's shell, which is
# ash rather than bash, so it stays POSIX: no arrays, no [[ ]].
set -euo pipefail

database_url=${GATEWAY_DATABASE_URL:-}
if [ -z "$database_url" ]; then
  printf '%s\n' 'Set GATEWAY_DATABASE_URL to the database to report on.' >&2
  exit 1
fi

query() {
  if command -v psql >/dev/null 2>&1; then
    psql "$database_url" -X -q -v ON_ERROR_STOP=1 --no-align --field-separator ' | ' --tuples-only -c "$1"
  else
    docker run --rm -i postgres:16-alpine psql "$database_url" -X -q -v ON_ERROR_STOP=1 --no-align --field-separator ' | ' --tuples-only -c "$1"
  fi
}

section() {
  title=$1
  guidance=$2
  rows=$(query "$3")
  printf '\n%s\n' "$title"
  printf '  what to do: %s\n' "$guidance"
  if [ -z "$(printf '%s' "$rows" | tr -d '[:space:]')" ]; then
    printf '  (none)\n'
    return
  fi
  printf '%s\n' "$rows" | while IFS= read -r row; do
    printf '  %s\n' "$row"
  done
}

printf '%s\n' 'reader/writer level migration report'
printf '  %s\n' 'Read-only. Run it before the upgrade for the inventory and after it to confirm.'
printf '  %s\n' 'Every row names the identifier to act on: an actor, an account, a key, or a repository.'

printf '\n1. Repository authority beyond the account level\n'
printf '  %s\n' 'Not applicable: the adopted plan converts a level into repository-wide grants, so a'
printf '  %s\n' 'materialized grant cannot reach further than the level it replaced. A plan that'
printf '  %s\n' 'assigned role templates instead would list the grants whose scope exceeds the level here.'

section '2. Principals that reach repositories only through the legacy static patterns' \
  'add each actor to the repository grant list it still depends on, or keep its pattern in GATEWAY_REPOSITORY_READERS / GATEWAY_REPOSITORY_WRITERS' \
  "SELECT a.actor,
          a.repository,
          count(*) AS decisions,
          to_char(max(a.occurred_at), 'YYYY-MM-DD\"T\"HH24:MI:SSZ') AS last_seen
   FROM resolver_audit_log a
   WHERE a.authorization_source = 'legacy_static'
     AND a.authorization_reason IN ('read_pattern_granted', 'write_pattern_granted')
     AND NOT EXISTS (
       SELECT 1 FROM repository_grants g
       JOIN hosted_repositories r ON r.id = g.repository_id
       WHERE g.principal = a.actor AND r.name = a.repository
     )
   GROUP BY a.actor, a.repository
   ORDER BY last_seen DESC, a.actor"

section '3. Grants held by principals without a user row' \
  'confirm each key or service account is meant to hold this repository, and revoke the credential rather than the grant when it is not' \
  "SELECT g.principal,
          CASE
            WHEN k.id IS NOT NULL THEN 'api key: ' || k.name || CASE WHEN k.revoked_at IS NOT NULL THEN ' (revoked)' ELSE '' END
            WHEN s.id IS NOT NULL THEN 'service account: ' || s.name || CASE WHEN s.state <> 'active' THEN ' (' || s.state || ')' ELSE '' END
            ELSE 'unresolved principal'
          END AS account,
          r.name AS repository,
          array_to_string(g.scopes, ',') AS scopes,
          COALESCE(NULLIF(g.resource_prefix, ''), '(whole repository)') AS scope_prefix
   FROM repository_grants g
   LEFT JOIN hosted_repositories r ON r.id = g.repository_id
   LEFT JOIN api_keys k ON g.principal = 'api-key:' || k.id::text
   LEFT JOIN service_accounts s ON g.principal = 'service-account:' || s.id::text
   WHERE g.principal LIKE 'api-key:%' OR g.principal LIKE 'service-account:%'
   ORDER BY g.principal, r.name"

section '4a. Accounts whose level the model cannot express' \
  'set each account to none, member, or admin; the upgrade narrows an unrecognized level to member, which denies more than it did' \
  "SELECT name AS account, role AS level, state
   FROM users
   WHERE role NOT IN ('none', 'member', 'admin')
   ORDER BY name"

section '4b. API keys whose levels the model cannot express' \
  'replace the key and assign one level, then grant it per repository; an unrecognized level is dropped and a set is collapsed to its highest' \
  "SELECT k.name AS api_key,
          array_to_string(k.roles, ',') AS levels,
          CASE
            WHEN cardinality(k.roles) > 1 THEN 'more than one level'
            ELSE 'unrecognized level'
          END AS reason
   FROM api_keys k
   WHERE cardinality(k.roles) > 1
      OR EXISTS (SELECT 1 FROM unnest(k.roles) AS level WHERE level NOT IN ('none', 'member', 'admin'))
   ORDER BY k.name"

section '4c. Grants that cannot take effect' \
  'delete the grant, or restore the repository it names' \
  "SELECT g.principal,
          COALESCE(r.name, '(repository missing)') AS repository,
          COALESCE(r.state, 'missing') AS state,
          array_to_string(g.scopes, ',') AS scopes
   FROM repository_grants g
   LEFT JOIN hosted_repositories r ON r.id = g.repository_id
   WHERE r.id IS NULL OR r.state <> 'active'
   ORDER BY repository, g.principal"

section '4d. Grants naming an account that does not exist' \
  'delete the grant or recreate the account; an actor without one of these prefixes is a token actor and is not listed' \
  "SELECT g.principal,
          r.name AS repository,
          array_to_string(g.scopes, ',') AS scopes
   FROM repository_grants g
   LEFT JOIN hosted_repositories r ON r.id = g.repository_id
   LEFT JOIN users u ON g.principal = 'user:' || u.name
   LEFT JOIN api_keys k ON g.principal = 'api-key:' || k.id::text
   LEFT JOIN service_accounts s ON g.principal = 'service-account:' || s.id::text
   WHERE (g.principal LIKE 'user:%' AND u.id IS NULL)
      OR (g.principal LIKE 'api-key:%' AND k.id IS NULL)
      OR (g.principal LIKE 'service-account:%' AND s.id IS NULL)
   ORDER BY g.principal, r.name"
