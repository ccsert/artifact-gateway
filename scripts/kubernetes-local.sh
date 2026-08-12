#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
namespace=artifact-gateway-local
overlay="$root/deploy/kubernetes/overlays/local"
gateway_image=artifact-gateway:k8s-local
console_image=artifact-gateway-console:k8s-local
console_port=18081
console_url="http://127.0.0.1:${console_port}"

postgres_password=${K8S_LOCAL_POSTGRES_PASSWORD:-}
minio_user=${K8S_LOCAL_MINIO_USER:-}
minio_password=${K8S_LOCAL_MINIO_PASSWORD:-}
admin_token=${K8S_LOCAL_ADMIN_TOKEN:-}
resolver_token=${K8S_LOCAL_RESOLVER_TOKEN:-}
settings_key=${K8S_LOCAL_SETTINGS_ENCRYPTION_KEY:-}

effective_secret_value() {
  local configured_value=$1 key=$2 local_default=$3 encoded
  if [[ -n "$configured_value" ]]; then
    printf '%s' "$configured_value"
    return
  fi
  encoded=$(kubectl -n "$namespace" get secret artifact-gateway-secrets \
    -o "jsonpath={.data.${key}}" 2>/dev/null || true)
  if [[ -n "$encoded" ]]; then
    printf '%s' "$encoded" | base64 --decode
    return
  fi
  printf '%s' "$local_default"
}

effective_admin_token() {
  effective_secret_value "$admin_token" GATEWAY_ADMIN_TOKEN local-gateway-admin-token
}

usage() {
  printf '%s\n' 'usage: scripts/kubernetes-local.sh check|up|status|verify|down'
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'required command not found: %s\n' "$1" >&2
    exit 1
  }
}

require_local_context() {
  local context
  context=$(kubectl config current-context)
  case "$context" in
    docker-desktop)
      ;;
    *)
      if [[ "${ARTIFACT_GATEWAY_ALLOW_NONLOCAL_K8S:-0}" != "1" ]]; then
        printf 'refusing to mutate non-local Kubernetes context %s; set ARTIFACT_GATEWAY_ALLOW_NONLOCAL_K8S=1 only after verifying the target\n' "$context" >&2
        exit 1
      fi
      ;;
  esac
}

validate_settings() {
  local configured_postgres_password configured_settings_key
  configured_postgres_password=$(effective_secret_value \
    "$postgres_password" POSTGRES_PASSWORD local-postgres-password)
  configured_settings_key=$(effective_secret_value \
    "$settings_key" GATEWAY_SETTINGS_ENCRYPTION_KEY 0123456789abcdef0123456789abcdef)
  if [[ ! "$configured_postgres_password" =~ ^[A-Za-z0-9._~-]+$ ]]; then
    printf 'K8S_LOCAL_POSTGRES_PASSWORD must contain only URL-safe unreserved characters\n' >&2
    exit 1
  fi
  if [[ ${#configured_settings_key} -ne 32 ]]; then
    printf 'K8S_LOCAL_SETTINGS_ENCRYPTION_KEY must be exactly 32 characters\n' >&2
    exit 1
  fi
}

require_console_port() {
  if kubectl -n "$namespace" get service artifact-gateway-console >/dev/null 2>&1; then
    return
  fi
  if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$console_port" -sTCP:LISTEN >/dev/null 2>&1; then
    printf 'local port %s is already in use; stop the listener before creating the Kubernetes stack\n' "$console_port" >&2
    exit 1
  fi
}

render_check() {
  "$root/scripts/kubernetes-local-manifest-test.sh"
}

build_images() {
  if [[ "${K8S_LOCAL_SKIP_BUILD:-0}" == "1" ]]; then
    return
  fi
  docker build -t "$gateway_image" "$root"
  docker build -f "$root/Dockerfile.console" -t "$console_image" "$root"
}

apply_runtime_inputs() {
  local configured_postgres_password configured_minio_user configured_minio_password
  local configured_admin_token configured_resolver_token configured_settings_key
  configured_postgres_password=$(effective_secret_value \
    "$postgres_password" POSTGRES_PASSWORD local-postgres-password)
  configured_minio_user=$(effective_secret_value \
    "$minio_user" MINIO_ROOT_USER local-minio-user)
  configured_minio_password=$(effective_secret_value \
    "$minio_password" MINIO_ROOT_PASSWORD local-minio-password)
  configured_admin_token=$(effective_admin_token)
  configured_resolver_token=$(effective_secret_value \
    "$resolver_token" GATEWAY_RESOLVER_TOKEN local-gateway-resolver-token)
  configured_settings_key=$(effective_secret_value \
    "$settings_key" GATEWAY_SETTINGS_ENCRYPTION_KEY 0123456789abcdef0123456789abcdef)
  kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$namespace" create secret generic artifact-gateway-secrets \
    --from-literal=POSTGRES_PASSWORD="$configured_postgres_password" \
    --from-literal=MINIO_ROOT_USER="$configured_minio_user" \
    --from-literal=MINIO_ROOT_PASSWORD="$configured_minio_password" \
    --from-literal=GATEWAY_DATABASE_URL="postgres://gateway:${configured_postgres_password}@postgres:5432/gateway?sslmode=disable" \
    --from-literal=GATEWAY_S3_ACCESS_KEY="$configured_minio_user" \
    --from-literal=GATEWAY_S3_SECRET_KEY="$configured_minio_password" \
    --from-literal=GATEWAY_ADMIN_TOKEN="$configured_admin_token" \
    --from-literal=GATEWAY_RESOLVER_TOKEN="$configured_resolver_token" \
    --from-literal=GATEWAY_SETTINGS_ENCRYPTION_KEY="$configured_settings_key" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$namespace" create configmap artifact-gateway-migrations \
    --from-file="$root/migrations" \
    --from-file=run-migrations.sh="$root/scripts/run-migrations.sh" \
    --dry-run=client -o yaml | kubectl apply -f -
}

wait_for_http() {
  local url=$1
  local attempts=${2:-120}
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 10 \
      --silent --show-error --fail "$url" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  printf 'timed out waiting for %s\n' "$url" >&2
  return 1
}

up() {
  require_command docker
  require_command kubectl
  require_command curl
  require_local_context
  validate_settings
  require_console_port
  render_check
  build_images
  apply_runtime_inputs
  kubectl apply -k "$overlay"
  kubectl -n "$namespace" rollout status statefulset/postgres --timeout=180s
  kubectl -n "$namespace" rollout status statefulset/minio --timeout=180s
  kubectl -n "$namespace" rollout status deployment/artifact-gateway --timeout=300s
  kubectl -n "$namespace" rollout status deployment/artifact-gateway-console --timeout=180s
  wait_for_http "$console_url/readyz"
  wait_for_http "$console_url/"
  local configured_admin_token
  configured_admin_token=$(effective_admin_token)
  curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --silent --show-error --fail \
    -H "Authorization: Bearer $configured_admin_token" \
    "$console_url/api/v2/formats" >/dev/null
  printf 'Artifact Gateway Kubernetes stack is ready at %s\n' "$console_url"
  printf 'Local administrator token: %s\n' "$configured_admin_token"
}

status() {
  require_command kubectl
  require_command curl
  require_local_context
  kubectl -n "$namespace" get pods,pvc,services
  curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --silent --show-error --fail "$console_url/readyz" >/dev/null
  curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --silent --show-error --fail "$console_url/" >/dev/null
  printf 'Artifact Gateway Kubernetes stack is ready at %s\n' "$console_url"
}

verify_persistence() {
  require_command kubectl
  require_command curl
  require_command jq
  require_local_context
  validate_settings

  local configured_admin_token run_id repository_name response repository_id body restored
  configured_admin_token=$(effective_admin_token)
  run_id="$(date +%s)-$$"
  repository_name="k8s-persistence-$run_id"
  body="Kubernetes persistence smoke $run_id"
  response=$(curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --silent --show-error --fail \
    -H "Authorization: Bearer $configured_admin_token" \
    -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $repository_name" \
    --data "$(printf '{\"name\":\"%s\",\"format\":\"raw\"}' "$repository_name")" \
    "$console_url/api/v2/repositories")
  repository_id=$(jq -er '.id' <<<"$response")

  curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --silent --show-error --fail \
    -H "Authorization: Bearer $configured_admin_token" \
    -H 'Content-Type: text/plain' \
    -X PUT --data-binary "$body" \
    "$console_url/raw/$repository_name/smoke/persistence.txt" >/dev/null

  kubectl -n "$namespace" rollout restart deployment/artifact-gateway
  kubectl -n "$namespace" rollout status deployment/artifact-gateway --timeout=300s
  wait_for_http "$console_url/readyz"

  curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --retry 10 --retry-all-errors --retry-delay 1 --silent --show-error --fail \
    -H "Authorization: Bearer $configured_admin_token" \
    "$console_url/api/v2/repositories/$repository_id" >/dev/null
  restored=$(curl --noproxy '127.0.0.1,localhost' --connect-timeout 3 --max-time 15 \
    --retry 10 --retry-all-errors --retry-delay 1 --silent --show-error --fail \
    -H "Authorization: Bearer $configured_admin_token" \
    "$console_url/raw/$repository_name/smoke/persistence.txt")
  if [[ "$restored" != "$body" ]]; then
    printf 'persisted Raw artifact changed after Gateway restart\n' >&2
    exit 1
  fi
  printf 'Kubernetes persistence verification passed for Repository %s\n' "$repository_name"
}

down() {
  require_command kubectl
  require_local_context
  kubectl delete namespace "$namespace" --ignore-not-found
}

case "${1:-}" in
  check)
    require_command kubectl
    render_check
    ;;
  up)
    up
    ;;
  status)
    status
    ;;
  verify)
    verify_persistence
    ;;
  down)
    down
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
