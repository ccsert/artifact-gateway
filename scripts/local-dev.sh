#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
environment_file=${GATEWAY_ENV_FILE:-$repo_root/.env}
state_dir=${GATEWAY_DEV_STATE_DIR:-$repo_root/.local-dev}
console_pid_file="$state_dir/console.pid"
console_log_file="$state_dir/console.log"
console_label="com.artifact-gateway.console.$(printf '%s' "$repo_root" | cksum | awk '{print $1}')"

read_local_dev_value() {
  local name=$1
  awk -F= -v name="$name" '$1 == name { print substr($0, index($0, "=") + 1); exit }' <<<"$local_dev_environment"
}

local_dev_environment=
if [[ -f "$environment_file" ]] && command -v docker >/dev/null 2>&1; then
  local_dev_environment=$(
    docker compose --env-file "$environment_file" -f "$repo_root/compose.yml" config 2>/dev/null |
      awk '
        $0 == "x-local-dev:" { local_dev = 1; next }
        local_dev && /^[^[:space:]]/ { exit }
        local_dev && ($1 == "gateway-port:" || $1 == "console-port:") {
          name = $1
          sub(/-port:$/, "", name)
          value = $2
          gsub(/^"|"$/, "", value)
          print name "=" value
        }
      ' || true
  )
fi

gateway_port=${GATEWAY_HTTP_PORT:-$(read_local_dev_value gateway)}
gateway_port=${gateway_port:-8080}
console_port=${GATEWAY_CONSOLE_PORT:-$(read_local_dev_value console)}
console_port=${console_port:-4173}
gateway_url="http://127.0.0.1:${gateway_port}"
console_url="http://127.0.0.1:${console_port}"

http_status() {
  curl --noproxy '*' --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 3 "$1" 2>/dev/null || true
}

check_endpoint() {
  local label=$1
  local url=$2
  local expected=$3
  local code
  code=$(http_status "$url")
  printf '%-8s %s  HTTP %s\n' "$label" "$url" "${code:-000}"
  [[ "$code" == "$expected" ]]
}

console_is_managed() {
  if [[ $(uname -s) == Darwin ]]; then
    launchctl print "gui/$(id -u)/$console_label" >/dev/null 2>&1
    return
  fi
  [[ -f "$console_pid_file" ]] || return 1
  local pid recorded_start_time current_start_time
  read -r pid recorded_start_time <"$console_pid_file" || return 1
  [[ "$pid" =~ ^[0-9]+$ && "$recorded_start_time" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  current_start_time=$(process_start_time "$pid") || return 1
  [[ "$current_start_time" == "$recorded_start_time" ]]
}

process_start_time() {
  local pid=$1
  local process_stat fields_after_name
  [[ -r "/proc/$pid/stat" ]] || return 1
  process_stat=$(<"/proc/$pid/stat")
  fields_after_name=${process_stat##*) }
  awk '{ print $20 }' <<<"$fields_after_name"
}

start_console() {
  local node_binary
  node_binary=$(command -v node || true)
  [[ -n "$node_binary" ]] || {
    printf 'Node.js is required to start the Console.\n' >&2
    return 1
  }
  if [[ ! -x "$repo_root/console/node_modules/.bin/vite" ]]; then
    npm --prefix "$repo_root/console" ci
  fi

  mkdir -p "$state_dir"
  if [[ $(uname -s) == Darwin ]]; then
    launchctl submit -l "$console_label" -o "$console_log_file" -e "$console_log_file" -- \
      /usr/bin/env VITE_GATEWAY_PROXY_TARGET="$gateway_url" \
      "$node_binary" "$repo_root/console/node_modules/.bin/vite" "$repo_root/console" --host 127.0.0.1 --port "$console_port" --strictPort
    return
  fi

  VITE_GATEWAY_PROXY_TARGET="$gateway_url" nohup \
    "$node_binary" "$repo_root/console/node_modules/.bin/vite" "$repo_root/console" --host 127.0.0.1 --port "$console_port" --strictPort >"$console_log_file" 2>&1 </dev/null &
  local pid=$!
  local start_time
  start_time=$(process_start_time "$pid") || {
    printf 'Cannot verify the Console process identity through /proc.\n' >&2
    return 1
  }
  printf '%s %s\n' "$pid" "$start_time" >"$console_pid_file"
}

stop_console() {
  if [[ $(uname -s) == Darwin ]]; then
    if console_is_managed; then
      launchctl remove "$console_label"
      for _ in {1..50}; do
        if ! console_is_managed && [[ $(http_status "$console_url/") != 200 ]]; then
          return 0
        fi
        sleep 0.1
      done
      printf 'Console supervisor did not stop within 5 seconds.\n' >&2
      return 1
    fi
    return 0
  fi
  if console_is_managed; then
    local pid _start_time
    read -r pid _start_time <"$console_pid_file"
    kill "$pid"
    for _ in {1..50}; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
    kill -0 "$pid" 2>/dev/null && {
      printf 'Console process did not stop within 5 seconds.\n' >&2
      return 1
    }
    rm -f "$console_pid_file"
  elif [[ -f "$console_pid_file" ]]; then
    rm -f "$console_pid_file"
  fi
}

start() {
  [[ -f "$environment_file" ]] || {
    printf 'Missing %s; copy .env.example and configure local credentials first.\n' "$environment_file" >&2
    return 1
  }

  docker compose --env-file "$environment_file" -f "$repo_root/compose.yml" up -d --build --wait --remove-orphans
  local console_code
  console_code=$(http_status "$console_url/")
  if console_is_managed; then
    stop_console
    console_code=000
  elif [[ "$console_code" == 200 ]]; then
    printf 'Console is already reachable at %s but is not managed by this checkout; it was left unchanged.\n' "$console_url"
    status
    return 0
  fi

  start_console
  for _ in {1..100}; do
    console_code=$(http_status "$console_url/")
    [[ "$console_code" == 200 ]] && break
    if ! console_is_managed; then
      printf 'Console exited before becoming ready. Recent log output:\n' >&2
      tail -n 40 "$console_log_file" >&2 || true
      return 1
    fi
    sleep 0.1
  done
  if [[ "$console_code" != 200 ]]; then
    stop_console || true
    printf 'Console did not become ready at %s within 10 seconds.\n' "$console_url" >&2
    return 1
  fi
  status
}

status() {
  local failed=0

  check_endpoint Console "$console_url/" 200 || failed=1
  check_endpoint 'Console API' "$console_url/api/v2/public/repositories" 200 || failed=1
  check_endpoint 'Gateway livez' "$gateway_url/livez" 204 || failed=1
  check_endpoint 'Gateway readyz' "$gateway_url/readyz" 204 || failed=1

  return "$failed"
}

stop() {
  [[ -f "$environment_file" ]] || {
    printf 'Missing %s; nothing was stopped.\n' "$environment_file" >&2
    return 1
  }
  stop_console
  printf 'Stopped Console; Gateway and data volumes remain running.\n'
}

case ${1:-} in
  start) start ;;
  status) status ;;
  stop) stop ;;
  *)
    printf 'usage: %s {start|status|stop}\n' "${0##*/}" >&2
    exit 2
    ;;
esac
