#!/usr/bin/env bash
set -euo pipefail
: "${VYOS_ENDPOINT:?Use a disposable isolated lab}"
: "${VYOS_API_KEY:?Set a test-only API key}"
: "${VYOS_CA_CERTIFICATE_FILE:?Set the lab CA bundle}"
if [[ "${1:-}" != --inside-lab ]]; then
    : "${VYOS_LAB_EXEC:?Set an executable wrapper that enters the isolated client network}"
    [[ -x "$VYOS_LAB_EXEC" ]] || { echo 'VYOS_LAB_EXEC must be executable' >&2; exit 1; }
    # The host runner can reach GitHub and the module proxy. The test binary and
    # Terraform are prepared here before the wrapper enters the offline network.
    tempdir=$(mktemp -d)
    trap 'rm -rf "$tempdir"' EXIT
    go test -c -o "$tempdir/provider-tests" ./internal/provider
    export TF_ACC_TERRAFORM_PATH="${TF_ACC_TERRAFORM_PATH:-$(command -v terraform)}"
    "$VYOS_LAB_EXEC" "$PWD/.ci/acceptance.sh" --inside-lab "$tempdir/provider-tests"
    exit
fi
: "${2:?Pass the prebuilt test binary}"
version=$(curl --fail --silent --show-error --max-time 15 --cacert "$VYOS_CA_CERTIFICATE_FILE" "${VYOS_ENDPOINT%/}/info?version=true")
python3 -c 'import json,sys; r=json.load(sys.stdin); assert r["success"] and r["data"]["version"]=="2026.10.22-0026-rolling", "wrong VyOS build"' <<<"$version"
TF_ACC=1 "$2" -test.count=1 -test.v -test.run TestAcc -test.timeout 15m
