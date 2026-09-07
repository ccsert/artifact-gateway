#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
mkdir "$workdir/bin"
export AG_ISOLATION_LOG="$workdir/docker-calls"
export AG_ISOLATION_MIGRATIONS
AG_ISOLATION_MIGRATIONS=$(find "$root/migrations" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' ')
cat >"$workdir/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == volume ]]; then
  exit 0
fi
[[ "$1" == compose ]] || exit 10
shift
# Every lifecycle call must override both the exported project variable and
# the checkout .env. Validate the public command boundary before simulating it.
[[ "$1" == --env-file && "$2" == /dev/null && "$3" == --project-name && "$4" == artifact-gateway-integration && "$5" == -f && "$6" == *compose.integration.yml ]] || exit 11
shift 6
printf '%s\n' "$*" >>"$AG_ISOLATION_LOG"
if [[ "$*" == *--volume* ]]; then
  printf 'Applied migration drift probe has changed\n' >&2
  # Match the real runner's migration filename in the diagnostic.
  printf 'Applied migration %s has changed\n' "${AG_ISOLATION_LATEST}" >&2
  exit 1
fi
if [[ "$*" == *migrate* ]]; then
  for ((i=0; i<AG_ISOLATION_MIGRATIONS; i++)); do
    printf 'Skipping applied migration fixture-%s.sql.\n' "$i"
  done
fi
SH
chmod +x "$workdir/bin/docker"
export PATH="$workdir/bin:$PATH"
export COMPOSE_PROJECT_NAME=existing-local-service-must-not-be-touched
export AG_ISOLATION_LATEST
AG_ISOLATION_LATEST=$(find "$root/migrations" -maxdepth 1 -name '*.sql' | sort | tail -1)
AG_ISOLATION_LATEST=${AG_ISOLATION_LATEST##*/}
"$root/scripts/integration-test.sh" >/dev/null
make --no-print-directory -C "$root" integration-down >/dev/null
for expected in 'down -v --remove-orphans' 'up -d --wait postgres rustfs' 'run --rm --no-deps test'; do
  grep -Fq -- "$expected" "$AG_ISOLATION_LOG"
done
printf 'Integration Compose isolation passed with an unrelated exported project name.\n'
