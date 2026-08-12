#!/usr/bin/env bash
set -euo pipefail

package_directory=${1:?usage: check-node-dependencies.sh PACKAGE_DIRECTORY EXECUTABLE...}
shift
[[ $# -gt 0 ]] || {
  printf 'At least one required executable must be provided.\n' >&2
  exit 2
}

npm_command=${NODE_DEPENDENCY_CHECK_NPM:-npm}
if [[ "$npm_command" == */* ]]; then
  [[ -x "$npm_command" ]] || {
    printf 'npm command is not executable: %s\n' "$npm_command" >&2
    exit 1
  }
elif ! command -v "$npm_command" >/dev/null 2>&1; then
  printf 'npm is required to validate Node.js dependencies.\n' >&2
  exit 1
fi

install_command="npm --prefix $package_directory ci --ignore-scripts --no-audit --no-fund"
for executable in "$@"; do
  if [[ ! -x "$package_directory/node_modules/.bin/$executable" ]]; then
    printf 'Node dependencies for %s are missing or do not match package.json.\n' "$package_directory" >&2
    printf 'Run: %s\n' "$install_command" >&2
    printf 'If the local Console is running, use `make dev-down` before reinstalling and `make dev` afterwards.\n' >&2
    exit 1
  fi
done

if ! "$npm_command" --prefix "$package_directory" ls --depth=0 --ignore-scripts >/dev/null 2>&1; then
  printf 'Node dependencies for %s are missing or do not match package.json.\n' "$package_directory" >&2
  printf 'Run: %s\n' "$install_command" >&2
  printf 'If the local Console is running, use `make dev-down` before reinstalling and `make dev` afterwards.\n' >&2
  exit 1
fi
