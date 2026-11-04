#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go test -c -o "$work/provider-tests" ./internal/provider
python3 .ci/vyos_vm.py run \
    --cache "${VYOS_CI_CACHE:-.ci-cache}" \
    --output "${VYOS_CI_OUTPUT:-.ci-results}" \
    --accel "${VYOS_CI_ACCEL:-auto}" \
    --test-binary "$work/provider-tests" \
    --terraform "${TF_ACC_TERRAFORM_PATH:-$(command -v terraform)}"
