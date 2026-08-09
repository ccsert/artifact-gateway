#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
namespace=${OIDC_K8S_NAMESPACE:-artifact-gateway-oidc-test}
image=${OIDC_TEST_IMAGE:-artifact-gateway:oidc-test}
gateway_port=${OIDC_TEST_GATEWAY_PORT:-18080}
keycloak_port=${OIDC_TEST_KEYCLOAK_PORT:-8081}
console_port=${OIDC_TEST_CONSOLE_PORT:-4173}
keep_namespace=${OIDC_TEST_KEEP_NAMESPACE:-0}
work_dir=$(mktemp -d)
processes=()

cleanup() {
  local status=$?
  for process in "${processes[@]:-}"; do
    kill "$process" >/dev/null 2>&1 || true
  done
  rm -rf "$work_dir"
  if [[ "$keep_namespace" != "1" ]]; then
    kubectl delete namespace "$namespace" --ignore-not-found --wait=false >/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'required command not found: %s\n' "$1" >&2
    exit 1
  }
}

require_command docker
require_command kubectl
require_command curl
require_command npm

for port in "$gateway_port" "$keycloak_port" "$console_port"; do
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    printf 'local port %s is already in use; set the corresponding OIDC_TEST_*_PORT variable.\n' "$port" >&2
    exit 1
  fi
done

docker build -t "$image" "$root"

kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
sed "s/127\\.0\\.0\\.1:4173/127.0.0.1:${console_port}/g" \
  "$root/deploy/oidc-test/artifact-gateway-realm.json" >"$work_dir/artifact-gateway-realm.json"
kubectl -n "$namespace" create configmap ag-oidc-keycloak-realm \
  --from-file=artifact-gateway-realm.json="$work_dir/artifact-gateway-realm.json" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$namespace" create configmap ag-oidc-test-config \
  --from-literal="GATEWAY_OIDC_REDIRECT_URL=http://127.0.0.1:${console_port}/auth/oidc/callback" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$namespace" create configmap ag-oidc-migrations \
  --from-file="$root/migrations" \
  --from-file=run-migrations.sh="$root/scripts/run-migrations.sh" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl kustomize "$root/deploy/oidc-test" \
  | sed "s/8081/${keycloak_port}/g" \
  | kubectl apply -f -

kubectl -n "$namespace" rollout status deployment/postgres --timeout=180s
kubectl -n "$namespace" rollout status deployment/minio --timeout=180s
kubectl -n "$namespace" rollout status deployment/gateway --timeout=300s

kubectl -n "$namespace" port-forward service/gateway "$gateway_port":8080 >"$work_dir/gateway-port-forward.log" 2>&1 &
processes+=("$!")
kubectl -n "$namespace" port-forward service/keycloak "$keycloak_port":"$keycloak_port" >"$work_dir/keycloak-port-forward.log" 2>&1 &
processes+=("$!")

until curl --silent --show-error --fail "http://127.0.0.1:${gateway_port}/readyz" >/dev/null; do sleep 1; done
until curl --silent --show-error --fail "http://127.0.0.1:${keycloak_port}/realms/artifact-gateway/.well-known/openid-configuration" >/dev/null; do sleep 1; done

VITE_GATEWAY_PROXY_TARGET="http://127.0.0.1:${gateway_port}" \
  npm --prefix "$root/console" run dev -- --host 127.0.0.1 --port "$console_port" >"$work_dir/console.log" 2>&1 &
processes+=("$!")
until curl --silent --show-error --fail "http://127.0.0.1:${console_port}" >/dev/null; do sleep 1; done

KEYCLOAK_OIDC_E2E=1 \
KEYCLOAK_OIDC_USERNAME=gateway-admin \
KEYCLOAK_OIDC_PASSWORD=gateway-oidc-test-password \
KEYCLOAK_OIDC_PORT="$keycloak_port" \
PLAYWRIGHT_EXTERNAL_SERVER=1 \
PLAYWRIGHT_PORT="$console_port" \
  npm --prefix "$root/console" run e2e -- e2e/oidc-keycloak.spec.ts --workers=1
