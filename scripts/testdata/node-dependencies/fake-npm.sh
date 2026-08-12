#!/usr/bin/env bash
set -euo pipefail

: "${NODE_DEPENDENCY_TEST_LOG:?}"
printf '%s\n' "$*" >>"$NODE_DEPENDENCY_TEST_LOG"
exit "${NODE_DEPENDENCY_TEST_EXIT:-0}"
