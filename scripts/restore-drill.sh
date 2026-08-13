#!/usr/bin/env bash
set -euo pipefail

environment_file=${GATEWAY_ENV_FILE:-.env}
backup_dir=${1:?usage: scripts/restore-drill.sh <backup-directory>}
compose=(docker compose --env-file "$environment_file" -f compose.yml)
shasum -a 256 --check "$backup_dir/SHA256SUMS"

"${compose[@]}" stop gateway rustfs
"${compose[@]}" exec -T postgres \
  pg_restore -U gateway -d gateway --clean --if-exists <"$backup_dir/gateway.dump"
rustfs_container=$("${compose[@]}" ps -aq rustfs)
test -n "$rustfs_container"
"${compose[@]}" run --rm --no-deps --entrypoint sh rustfs \
  -ec 'find /data -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +'
docker cp - "$rustfs_container:/data" <"$backup_dir/rustfs-data.tar"
docker start "$rustfs_container" >/dev/null
if ! "${compose[@]}" run --rm --no-deps rustfs-ready; then
  printf 'Restored RustFS did not become healthy before the readiness deadline.\n' >&2
  "${compose[@]}" ps rustfs >&2 || true
  "${compose[@]}" logs --tail=50 rustfs >&2 || true
  exit 1
fi
# Starting through Compose re-evaluates the completed migrate dependency on
# some Compose releases. The restored dump already contains its schema, so
# restart only the existing Gateway container.
gateway_container=$("${compose[@]}" ps -aq gateway)
test -n "$gateway_container"
docker start "$gateway_container" >/dev/null
ready_status=000
gateway_url=
for _ in $(seq 1 80); do
  gateway_binding=$("${compose[@]}" port gateway 8080 | tail -n 1)
  if [[ -n "$gateway_binding" ]]; then
    gateway_url="http://127.0.0.1:${gateway_binding##*:}"
    ready_status=$(curl --noproxy '*' --silent --show-error --output /dev/null --write-out '%{http_code}' \
      --max-time 2 "$gateway_url/readyz" 2>/dev/null || true)
    [[ "$ready_status" == 204 ]] && break
  fi
  sleep 0.25
done
[[ "$ready_status" == 204 ]] || { printf 'Restored Gateway readiness returned HTTP %s at %s.\n' "$ready_status" "${gateway_url:-unresolved endpoint}" >&2; exit 1; }
printf 'Restore completed from: %s\n' "$backup_dir"
