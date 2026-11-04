#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
build=${VYOS_CI_BUILD:-$work/build.json}
if [ -z "${VYOS_CI_BUILD:-}" ]; then
    python3 .ci/vyos_vm.py resolve --release "${VYOS_CI_RELEASE:-latest}" --output "$build"
fi
go test -c -o "$work/provider-tests" ./internal/provider
python3 .ci/vyos_vm.py run \
    --build "$build" \
    --cache "${VYOS_CI_CACHE:-.ci-cache}" \
    --output "${VYOS_CI_OUTPUT:-.ci-results}" \
    --test-binary "$work/provider-tests" \
    --terraform "${TF_ACC_TERRAFORM_PATH:-$(command -v terraform)}"
