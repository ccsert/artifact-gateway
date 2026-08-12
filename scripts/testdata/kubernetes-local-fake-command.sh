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
    if [[ "$url" == */api/v2/repositories ]]; then
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
        cat "$FAKE_K8S_STATE_DIR/raw-body"
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
        *MINIO_ROOT_USER*) value=${FAKE_K8S_MINIO_USER:-} ;;
        *MINIO_ROOT_PASSWORD*) value=${FAKE_K8S_MINIO_PASSWORD:-} ;;
        *GATEWAY_ADMIN_TOKEN*) value=${FAKE_K8S_ADMIN_TOKEN:-} ;;
        *GATEWAY_RESOLVER_TOKEN*) value=${FAKE_K8S_RESOLVER_TOKEN:-} ;;
        *GATEWAY_SETTINGS_ENCRYPTION_KEY*) value=${FAKE_K8S_SETTINGS_KEY:-} ;;
        *) value= ;;
      esac
      if [[ -n "$value" ]]; then
        printf '%s' "$value" | base64
      fi
    elif [[ " $* " == *" get service artifact-gateway-console "* ]]; then
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
