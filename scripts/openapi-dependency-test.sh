#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
subject="$repo_root/scripts/check-node-dependencies.sh"
fake_npm="$repo_root/scripts/testdata/node-dependencies/fake-npm.sh"
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

mkdir -p "$workdir/console/node_modules/.bin"
ln -s /usr/bin/true "$workdir/console/node_modules/.bin/openapi-ts"
ln -s /usr/bin/true "$workdir/console/node_modules/.bin/prettier"
export NODE_DEPENDENCY_CHECK_NPM="$fake_npm"
export NODE_DEPENDENCY_TEST_LOG="$workdir/npm.log"

bash "$subject" "$workdir/console" openapi-ts prettier
grep -Fq -- "--prefix $workdir/console ls --depth=0" "$NODE_DEPENDENCY_TEST_LOG" || fail 'dependency check did not validate the installed tree'
if grep -Eq '(^| )ci( |$)' "$NODE_DEPENDENCY_TEST_LOG"; then
  fail 'dependency check mutated node_modules with npm ci'
fi

dry_run=$(make -n -C "$repo_root" openapi-check)
[[ "$dry_run" == *'check-node-dependencies.sh'* ]] || fail 'openapi-check does not validate dependencies'
if grep -Eq 'npm .* ci( |$)' <<<"$dry_run"; then
  fail 'openapi-check mutates node_modules with npm ci'
fi

rm "$workdir/console/node_modules/.bin/openapi-ts"
if output=$(bash "$subject" "$workdir/console" openapi-ts prettier 2>&1); then
  fail 'missing code generator passed dependency validation'
fi
[[ "$output" == *'npm --prefix'*' ci '* ]] || fail 'missing dependency error omitted the install command'

ln -s /usr/bin/true "$workdir/console/node_modules/.bin/openapi-ts"
export NODE_DEPENDENCY_TEST_EXIT=1
if output=$(bash "$subject" "$workdir/console" openapi-ts prettier 2>&1); then
  fail 'invalid dependency tree passed validation'
fi
[[ "$output" == *'do not match package.json'* ]] || fail 'invalid dependency tree returned an unclear error'

printf 'OpenAPI dependency tests passed\n'
