#!/usr/bin/env bash
set -euo pipefail

state=${LOCAL_DEV_TEST_STATE:?}
mode=${LOCAL_DEV_TEST_MODE:-managed}
command_name=${0##*/}

case "$command_name" in
  curl)
    url=${!#}
    case "$url" in
      */api/v2/public/repositories)
        if [[ "$mode" == proxy-failed ]]; then
          printf '500'
        elif [[ "$mode" == unmanaged || -f "$state/managed" ]]; then
          printf '200'
        else
          printf '000'
        fi
        ;;
      */livez | */readyz)
        printf '204'
        ;;
      */)
        if [[ "$mode" == unmanaged || -f "$state/managed" ]]; then
          printf '200'
        else
          printf '000'
        fi
        ;;
      *) printf '404' ;;
    esac
    ;;
  docker)
    printf '%s\n' "$*" >>"$state/docker.log"
    if [[ "$*" == *' config'* ]]; then
      printf '%s\n' 'x-local-dev:' '  gateway-port: "18081"' '  console-port: "4174"'
    fi
    ;;
  id)
    [[ ${1:-} == -u ]] && printf '501\n'
    ;;
  launchctl)
    printf '%s\n' "$*" >>"$state/launchctl.log"
    case ${1:-} in
      print) [[ -f "$state/managed" ]] ;;
      submit)
        if [[ "$mode" != console-exits ]]; then
          touch "$state/managed"
        fi
        ;;
      remove) rm -f "$state/managed" ;;
      *) exit 2 ;;
    esac
    ;;
  node | npm)
    printf '%s %s\n' "$command_name" "$*" >>"$state/runtime.log"
    ;;
  uname)
    printf '%s\n' "${LOCAL_DEV_TEST_OS:-Darwin}"
    ;;
  *)
    printf 'unsupported fake command: %s\n' "$command_name" >&2
    exit 2
    ;;
esac
