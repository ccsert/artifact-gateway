#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script="$root/scripts/kubernetes-local.sh"
real_kubectl=$(command -v kubectl)
workdir=$(mktemp -d)
fake_bin="$workdir/bin"
log="$workdir/commands.log"
state="$workdir/state"
mkdir -p "$fake_bin" "$state"
trap 'rm -rf "$workdir"' EXIT

for command_name in kubectl docker curl lsof; do
  ln -s "$root/scripts/testdata/kubernetes-local-fake-command.sh" "$fake_bin/$command_name"
done

export PATH="$fake_bin:$PATH"
export REAL_KUBECTL=$real_kubectl
export FAKE_K8S_LOG="$log"
export FAKE_K8S_STATE_DIR="$state"
export K8S_LOCAL_SKIP_BUILD=1

fail() {
  printf 'kubernetes-local test failed: %s\n' "$1" >&2
  exit 1
}

run_expect() {
  local expected=$1
  shift
  set +e
  "$@" >"$workdir/stdout" 2>"$workdir/stderr"
  local actual=$?
  set -e
  [[ "$actual" -eq "$expected" ]] || {
    cat "$workdir/stdout" "$workdir/stderr" >&2
    fail "expected exit $expected, got $actual from $*"
  }
}

assert_log() {
  grep -Fq -- "$1" "$log" || {
    cat "$log" >&2
    fail "missing command log entry: $1"
  }
}

[[ -x "$real_kubectl" ]] || fail 'real kubectl is required'

: >"$log"
run_expect 0 "$script" check
grep -Fq 'local Kubernetes manifest checks passed' "$workdir/stdout" || fail 'check did not render manifests'

: >"$log"
FAKE_K8S_CONTEXT=production run_expect 1 "$script" status
grep -Fq 'refusing to mutate non-local Kubernetes context production' "$workdir/stderr" || fail 'non-local context was not rejected'
if grep -Fq ' get pods,pvc,services' "$log"; then
  fail 'status queried workloads after rejecting the context'
fi

: >"$log"
K8S_LOCAL_POSTGRES_PASSWORD='unsafe/password' run_expect 1 "$script" up
grep -Fq 'must contain only URL-safe unreserved characters' "$workdir/stderr" || fail 'unsafe database password was accepted'

: >"$log"
K8S_LOCAL_SETTINGS_ENCRYPTION_KEY='short' run_expect 1 "$script" up
grep -Fq 'must be exactly 32 characters' "$workdir/stderr" || fail 'invalid settings key was accepted'

: >"$log"
K8S_LOCAL_RUSTFS_RPC_SECRET='short' run_expect 1 "$script" up
grep -Fq 'must be at least 32 characters' "$workdir/stderr" || fail 'short RustFS RPC secret was accepted'

: >"$log"
K8S_LOCAL_RUSTFS_SECRET_KEY='same-rustfs-secret-value-12345678' \
K8S_LOCAL_RUSTFS_RPC_SECRET='same-rustfs-secret-value-12345678' run_expect 1 "$script" up
grep -Fq 'must differ from K8S_LOCAL_RUSTFS_SECRET_KEY' "$workdir/stderr" || fail 'reused RustFS RPC secret was accepted'

: >"$log"
FAKE_K8S_LEGACY_MINIO=1 run_expect 1 "$script" up
grep -Fq 'legacy MinIO StatefulSet or data PVC detected' "$workdir/stderr" || fail 'legacy MinIO stack was overwritten in place'
if grep -Fq 'kubectl apply -k' "$log"; then
  fail 'RustFS manifest was applied over a legacy MinIO stack'
fi

: >"$log"
FAKE_K8S_LEGACY_MINIO_PVC=1 run_expect 1 "$script" up
grep -Fq 'legacy MinIO StatefulSet or data PVC detected' "$workdir/stderr" || fail 'orphaned MinIO PVC was ignored'

: >"$log"
FAKE_K8S_LEGACY_MINIO=1 K8S_LOCAL_RUSTFS_MIGRATION_CONFIRMED=1 run_expect 1 "$script" up
grep -Fq 'verified manifest fingerprint' "$workdir/stderr" || fail 'bare RustFS migration confirmation was accepted'
if grep -Fq 'kubectl apply -k' "$log"; then
  fail 'RustFS manifest was applied without a verified migration manifest fingerprint'
fi

: >"$log"
FAKE_K8S_LEGACY_MINIO=1 \
K8S_LOCAL_RUSTFS_MIGRATION_CONFIRMED=1 \
K8S_LOCAL_RUSTFS_MIGRATION_MANIFEST_SHA256=unverified run_expect 1 "$script" up
grep -Fq 'verified manifest fingerprint' "$workdir/stderr" || fail 'invalid RustFS migration fingerprint was accepted'

: >"$log"
migration_fingerprint="sha256:$(printf 'b%.0s' {1..64})"
FAKE_K8S_LEGACY_MINIO=1 \
K8S_LOCAL_RUSTFS_MIGRATION_CONFIRMED=1 \
K8S_LOCAL_RUSTFS_MIGRATION_MANIFEST_SHA256="$migration_fingerprint" run_expect 0 "$script" up
assert_log 'kubectl apply -k'

: >"$log"
FAKE_K8S_PORT_BUSY=1 run_expect 1 "$script" up
grep -Fq 'local port 80 is already in use' "$workdir/stderr" || fail 'occupied Ingress port was accepted'

: >"$log"
run_expect 0 "$script" up
assert_log 'kubectl apply -k'
assert_log 'kubectl -n artifact-gateway-local rollout status statefulset/postgres --timeout=180s'
assert_log 'kubectl -n artifact-gateway-local rollout status statefulset/rustfs --timeout=180s'
assert_log 'kubectl -n artifact-gateway-local rollout status deployment/artifact-gateway --timeout=300s'
assert_log 'kubectl -n artifact-gateway-local rollout status deployment/artifact-gateway-ingress --timeout=180s'
grep -Fq 'Artifact Gateway Kubernetes stack is ready at http://artifact-gateway.localhost' "$workdir/stdout" || fail 'up did not report the Ingress endpoint'

: >"$log"
FAKE_K8S_POSTGRES_PASSWORD='persisted-postgres-password' \
FAKE_K8S_RUSTFS_ACCESS_KEY='persisted-rustfs-user' \
FAKE_K8S_RUSTFS_SECRET_KEY='persisted-rustfs-password' \
FAKE_K8S_RUSTFS_RPC_SECRET='persisted-rustfs-independent-rpc-secret' \
FAKE_K8S_ADMIN_TOKEN='persisted-admin-token' \
FAKE_K8S_RESOLVER_TOKEN='persisted-resolver-token' \
FAKE_K8S_SETTINGS_KEY='abcdef0123456789abcdef0123456789' \
  run_expect 0 "$script" up
assert_log '--from-literal=POSTGRES_PASSWORD=persisted-postgres-password'
assert_log '--from-literal=RUSTFS_ACCESS_KEY=persisted-rustfs-user'
assert_log '--from-literal=RUSTFS_SECRET_KEY=persisted-rustfs-password'
assert_log '--from-literal=RUSTFS_RPC_SECRET=persisted-rustfs-independent-rpc-secret'
assert_log '--from-literal=GATEWAY_ADMIN_TOKEN=persisted-admin-token'
assert_log '--from-literal=GATEWAY_RESOLVER_TOKEN=persisted-resolver-token'
assert_log '--from-literal=GATEWAY_SETTINGS_ENCRYPTION_KEY=abcdef0123456789abcdef0123456789'

: >"$log"
run_expect 0 "$script" status
assert_log 'kubectl -n artifact-gateway-local get pods\,pvc\,services\,ingresses'

: >"$log"
FAKE_K8S_ADMIN_TOKEN='persisted-custom-token' run_expect 0 "$script" verify
assert_log 'kubectl -n artifact-gateway-local rollout restart deployment/artifact-gateway'
assert_log 'kubectl -n artifact-gateway-local rollout status deployment/artifact-gateway --timeout=300s'
assert_log 'curl --noproxy artifact-gateway.localhost\,127.0.0.1\,localhost --connect-timeout 3 --max-time 15 --silent --show-error --fail -H Authorization:\ Bearer\ persisted-custom-token'
assert_log 'persistence%3Bencoded.txt'
assert_log '--head'
assert_log '--range 0-9'
assert_log '@scope%2Fwidget'
assert_log 'http://artifact-gateway.localhost/v2/'
assert_log '/apt/ingress-missing/dists/stable/Release'
assert_log 'rejected%5Cpath'
assert_log 'rejected%00path'
grep -Fq 'Kubernetes persistence and Ingress protocol verification passed' "$workdir/stdout" || fail 'verify did not confirm persistence and protocol routing'

: >"$log"
run_expect 0 "$script" down
assert_log 'kubectl delete ingressclass artifact-gateway-local --ignore-not-found'
assert_log 'kubectl delete clusterrolebinding artifact-gateway-local-ingress-class-reader --ignore-not-found'
assert_log 'kubectl delete clusterrole artifact-gateway-local-ingress-class-reader --ignore-not-found'
assert_log 'kubectl delete namespace artifact-gateway-local --ignore-not-found'
[[ $(wc -l <"$log" | tr -d ' ') -eq 5 ]] || fail 'down issued commands outside context check and exact local resource deletion'

run_expect 2 "$script" unsupported
printf 'kubernetes-local CLI tests passed\n'
