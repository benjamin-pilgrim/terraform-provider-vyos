#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
child=
cleanup() {
    local status=$?
    trap - EXIT
    trap '' INT TERM
    if [ -n "$child" ]; then
        kill -TERM "$child" 2>/dev/null || true
        wait "$child" 2>/dev/null || true
    fi
    rm -rf "$work"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
run_child() {
    "$@" &
    child=$!
    wait "$child"
    child=
}
build=${VYOS_CI_BUILD:-$work/build.json}
if [ -z "${VYOS_CI_BUILD:-}" ]; then
    run_child python3 .ci/vyos_vm.py resolve --release "${VYOS_CI_RELEASE:-latest}" --output "$build"
fi
run_child go test -c -o "$work/provider-tests" ./internal/provider
run_child python3 .ci/vyos_vm.py run \
    --build "$build" \
    --cache "${VYOS_CI_CACHE:-.ci-cache}" \
    --output "${VYOS_CI_OUTPUT:-.ci-results}" \
    --test-binary "$work/provider-tests" \
    --terraform "${TF_ACC_TERRAFORM_PATH:-$(command -v terraform)}"
