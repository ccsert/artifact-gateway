#!/usr/bin/env bash
# Runs the checks the CI workflow runs, locally, grouped by CI job so the output
# maps one-to-one onto the GitHub checks. It calls the same make targets and
# scripts CI calls rather than reimplementing them, so a local pass means what a
# CI pass means.
#
# Usage:
#   scripts/ci-local.sh                 # fast set: the cheap checks that catch most regressions
#   scripts/ci-local.sh --job quality   # one CI job (quality | contract | console)
#   scripts/ci-local.sh --job all       # everything CI runs on a pull request
#   scripts/ci-local.sh --list          # show what each selection runs
#
# Options:
#   --job <name>      quality, contract, console, all (default: fast)
#   --lane <name>     one isolated contract lane; use --list for names
#   --keep-going      run every selected check instead of stopping at the first failure
#   --with-console-e2e  include Console E2E, which needs a running gateway stack
#
# The mainline-assets and mainline-images jobs are push-to-main only and cannot be
# reproduced from a feature branch; they are reported as out of scope.
set -uo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

job=fast
keep_going=0
with_console_e2e=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --job) [[ $# -ge 2 ]] || { printf "--job requires a value\n" >&2; exit 2; }; job=$2; shift 2 ;;
    --lane) [[ $# -ge 2 ]] || { printf "--lane requires a value\n" >&2; exit 2; }; job=lane:$2; shift 2 ;;
    --keep-going) keep_going=1; shift ;;
    --with-console-e2e) with_console_e2e=1; shift ;;
    --list) job=list; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

failures=()
ran=0
skipped=()
total_start=$SECONDS

run() {
  local label=$1
  shift
  local start=$SECONDS
  printf '  ... %s\n' "$label"
  if "$@"; then
    printf '  PASS %-46s %3ds\n' "$label" "$((SECONDS - start))"
    ran=$((ran + 1))
    return 0
  fi
  printf '  FAIL %-46s %3ds\n' "$label" "$((SECONDS - start))"
  ran=$((ran + 1))
  failures+=("$label")
  if [[ $keep_going -eq 0 ]]; then
    printf '\nStopped at the first failure. Re-run with --keep-going to collect the rest.\n'
    summary
    exit 1
  fi
  return 0
}

skip() {
  printf '  SKIP %-46s %s\n' "$1" "$2"
  skipped+=("$1: $2")
}

summary() {
  printf '\n--- summary (%ds total) ---\n' "$((SECONDS - total_start))"
  printf 'ran %d check(s), %d failure(s)\n' "$ran" "${#failures[@]}"
  if [[ ${#failures[@]} -gt 0 ]]; then
    printf 'failed:\n'
    printf '  - %s\n' "${failures[@]}"
  fi
  if [[ ${#skipped[@]} -gt 0 ]]; then
    printf 'skipped:\n'
    printf '  - %s\n' "${skipped[@]}"
  fi
  printf 'not reproducible locally: mainline-assets, mainline-images (push-to-main only).\n'
}

# CI runs `make openapi-check`, whose final step diffs the generated artifacts and
# therefore only works on a clean checkout. Locally the generated files are part
# of the change under review, so assert the equivalent property: regenerating must
# not change what is already on disk.
check_openapi_fresh() {
  python3 scripts/ci-contract.py openapi-fresh
}

gateway_ready() {
  local port=${GATEWAY_HTTP_PORT:-8080}
  curl -sf "http://127.0.0.1:${port}/readyz" >/dev/null 2>&1
}

run_quality() {
  printf '\n== quality ==\n'
  run "npm lockfile sources" node scripts/npm-lockfile-check.mjs
  run "go lint" make lint
  run "go vet" make vet
  run "release version and artifact builder" make release-version-check release-artifacts-test
  run "race detector" make race
  run "coverage floor" make coverage
  run "dependency audit" make dependency-audit
}

run_contract_lane() {
  local lane=$1
  local evidence="output/ci-contract-local/${lane}-${BASHPID}-${RANDOM}"
  local args=(run "$lane" --local --output "$evidence")
  if [[ $keep_going -eq 1 ]]; then args+=(--keep-going); fi
  run "contract lane: $lane" python3 scripts/ci-contract.py "${args[@]}"
}

run_contract() {
  printf '\n== contract-and-test (core plus every native lane) ==\n'
  run_contract_lane core
  while IFS= read -r lane; do
    run_contract_lane "$lane"
  done < <(python3 -c 'import json; print("\n".join(json.load(open("scripts/ci-contract-lanes.json"))["nativeOrder"]))')
  # Keep the existing additional local CLI stack check; CI runs this client
  # against the Console job's gateway instead.
  run "agctl CLI E2E (isolated Compose stack)" make cli-e2e
}

run_console() {
  printf '\n== console ==\n'
  run "npm lockfile sources" node scripts/npm-lockfile-check.mjs
  run "generated API client" make console-api-check
  run "type check" make console-typecheck
  run "lint and formatting" make console-check
  run "unit tests" make console-test
  run "build" make console-build
  run "deployment image" make console-docker-build
  if [[ $with_console_e2e -eq 1 ]]; then
    if gateway_ready; then
      run "console E2E" make console-e2e
    else
      skip "console E2E" "gateway not ready on 127.0.0.1:${GATEWAY_HTTP_PORT:-8080}; start it with \`make dev-bootstrap && make dev\`"
    fi
  else
    skip "console E2E" "pass --with-console-e2e and start the gateway stack to include it"
  fi
}

run_fast() {
  printf '\n== fast (inner loop) ==\n'
  run "go lint" make lint
  run "go vet" make vet
  run "go tests" env GOFLAGS= go test ./... -count=1
  run "documentation links and capabilities" make docs-check
  run "CLI command map contract" make cli-contract-check
  run "openapi generated artifacts fresh" check_openapi_fresh
  run "console type check" make console-typecheck
  run "console lint and formatting" make console-check
  run "console unit tests" make console-test
}

case "$job" in
  list)
    printf 'fast     : lint, vet, go test ./..., docs-check, CLI command map contract, openapi freshness, console typecheck/check/test\n'
    printf 'quality  : CI job quality\n'
    printf 'contract : core plus all six native lanes, then the local CLI stack check\n'
    printf 'lanes    : use --lane core|migration-clients|inventory|nuget|oci-migration|maven-oci-auth|six-format-auth\n'
    printf 'console  : CI job console\n'
    printf 'all      : quality, contract, console\n'
    ;;
  lane:*) run_contract_lane "${job#lane:}" ;;
  fast) run_fast ;;
  quality) run_quality ;;
  contract) run_contract ;;
  console) run_console ;;
  all)
    run_quality
    run_contract
    run_console
    ;;
  *) printf 'Unknown job: %s\n' "$job" >&2; exit 2 ;;
esac

summary
if [[ ${#failures[@]} -gt 0 ]]; then
  exit 1
fi
