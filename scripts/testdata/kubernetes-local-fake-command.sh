#!/usr/bin/env bash
set -euo pipefail

command_name=$(basename "$0")
{
  printf '%s' "$command_name"
  printf ' %q' "$@"
  printf '\n'
} >>"$FAKE_K8S_LOG"

case "$command_name" in
  docker)
    exit 0
    ;;
  lsof)
    [[ "${FAKE_K8S_PORT_BUSY:-0}" == "1" ]]
    ;;
  curl)
    url=${!#}
    if [[ " $* " == *" --write-out "* ]]; then
      case "$url" in
        *%5C* | *%00*) printf '400' ;;
        */v2/) printf '401' ;;
        */npm/* | */apt/*) printf '404' ;;
        *) printf '200' ;;
      esac
    elif [[ "$url" == */api/v2/repositories ]]; then
      printf '{"id":"fake-repository-id"}\n'
    elif [[ "$url" == */raw/* ]]; then
      if [[ " $* " == *" -X PUT "* ]]; then
        for ((index = 1; index <= $#; index++)); do
          if [[ "${!index}" == "--data-binary" ]]; then
            value_index=$((index + 1))
            printf '%s' "${!value_index}" >"$FAKE_K8S_STATE_DIR/raw-body"
            break
          fi
        done
      elif [[ -f "$FAKE_K8S_STATE_DIR/raw-body" ]]; then
        if [[ " $* " == *" --range 0-9 "* ]]; then
          printf 'Kubernetes'
        else
          cat "$FAKE_K8S_STATE_DIR/raw-body"
        fi
      fi
    fi
    ;;
  kubectl)
    if [[ "$*" == "config current-context" ]]; then
      printf '%s\n' "${FAKE_K8S_CONTEXT:-docker-desktop}"
    elif [[ "${1:-}" == "kustomize" || ( "${1:-}" == "create" && " $* " == *" --dry-run=client "* ) ]]; then
      exec "$REAL_KUBECTL" "$@"
    elif [[ "${1:-}" == "apply" && "${2:-}" == "-f" && "${3:-}" == "-" ]]; then
      cat >/dev/null
    elif [[ " $* " == *" get secret artifact-gateway-secrets "* ]]; then
      case "$*" in
        *POSTGRES_PASSWORD*) value=${FAKE_K8S_POSTGRES_PASSWORD:-} ;;
        *RUSTFS_ACCESS_KEY*) value=${FAKE_K8S_RUSTFS_ACCESS_KEY:-} ;;
        *RUSTFS_SECRET_KEY*) value=${FAKE_K8S_RUSTFS_SECRET_KEY:-} ;;
        *RUSTFS_RPC_SECRET*) value=${FAKE_K8S_RUSTFS_RPC_SECRET:-} ;;
        *GATEWAY_ADMIN_TOKEN*) value=${FAKE_K8S_ADMIN_TOKEN:-} ;;
        *GATEWAY_RESOLVER_TOKEN*) value=${FAKE_K8S_RESOLVER_TOKEN:-} ;;
        *GATEWAY_SETTINGS_ENCRYPTION_KEY*) value=${FAKE_K8S_SETTINGS_KEY:-} ;;
        *GATEWAY_APT_SIGNER_TOKEN*) value=${FAKE_K8S_APT_SIGNER_TOKEN:-} ;;
        *) value= ;;
      esac
      if [[ -n "$value" ]]; then
        printf '%s' "$value" | base64
      fi
    elif [[ " $* " == *" get statefulset minio "* ]]; then
      [[ "${FAKE_K8S_LEGACY_MINIO:-0}" == "1" ]]
    elif [[ " $* " == *" get persistentvolumeclaim data-minio-0 "* ]]; then
      [[ "${FAKE_K8S_LEGACY_MINIO_PVC:-0}" == "1" ]]
    elif [[ " $* " == *" get service artifact-gateway-console "* || " $* " == *" get service artifact-gateway-ingress "* ]]; then
      exit 1
    else
      exit 0
    fi
    ;;
  *)
    printf 'unexpected fake command: %s\n' "$command_name" >&2
    exit 1
    ;;
esac
