# Provider repair validation — 4 November 2026

Base: `53486a373df8f32f751865e3b5eaf0580dd622f9` (current fetched main).
Compiler: Go 1.27.3 on Linux amd64. Module minimum: Go 1.26.0.
Framework: 1.19.0; Plugin Testing: 1.16.0; indirect SDK v2: 2.40.1.

## Local deterministic checks

| Check | Result |
| --- | --- |
| `go test -race -count=1 -cover ./...` | PASS; provider 66.0%, VyOS HTTP/config layer 93.1% statement coverage |
| `go vet ./...` | PASS |
| `go mod tidy -diff` | PASS, no changes |
| `govulncheck ./...` | PASS: **No vulnerabilities found** |
| `go generate ./...`, compared with prior generated-file hashes | PASS, stable output |
| `actionlint` 1.7.12 | PASS |
| GoReleaser 2.20.0 `check` | PASS |
| `bash -n .ci/acceptance.sh`, `git diff --check` | PASS |

Without `TF_ACC`, the three real acceptance tests intentionally skip. That is
separate from the executed real-VM evidence below; passing unit tests alone is
not evidence of VyOS compatibility.

Before repair, the new fake-server regressions reproduced four failures: HTTP
500 with a success-shaped JSON body, HTTP 200 with `success:false`, a missing
success field, and stale configuration after external change. All now pass.
The suite also covers all mutation/save modes, errors before/during/after commit,
malformed JSON and configuration trees, secret redaction, secure TLS/custom CA/
explicit insecure mode/hostname/redirect handling, provider settings, concurrent
resources, competing creates, unsafe paths, ownership overlap, and null/flag/list
semantics.

## Real isolated VyOS evidence

VM: **2026.10.22-0026-rolling**, verified using its HTTPS `/info` response.
Rootless QEMU with isolated client network namespaces and no production or
physical NIC connection. Terraform/provider ran in the isolated management
client namespace, using dependencies built/cached on the host.

The API was bootstrapped with a test-only key, a SAN-matching self-signed
certificate, and current `service https api rest` / PKI syntax. Certificate and
hostname verification remained enabled with an explicit CA bundle. No
production configuration or credentials were copied into this repository.
The pinned `BaseConfigureModel.value: StrictStr = None` model and successful
runtime operations confirm that the separate `value` payload field is accepted.

Integration used Terraform **1.17.0**. Final scripted acceptance used **1.17.1**,
downloaded from HashiCorp and verified against its official SHA256 manifest.
The prepared-host / isolated-execution path in `.ci/acceptance.sh` is exercised
using a local lab adapter; the GitHub-hosted workflow itself was not run remotely.

| Real scenario | Result |
| --- | --- |
| Concurrent creation of three disjoint scalar/subtree, list and omitted-value flag resources (`-parallelism=10`) | PASS |
| Native active **and** saved configuration after create/update/delete | PASS |
| No-op after creation, including omitted flag and null object member | PASS |
| Import and immediate no-op with canonical complete subtree | PASS |
| External drift detected and reconciled; external deletion detected and recreated | PASS |
| Unrelated firewall sentinel and pre-existing SSH security flag preserved | PASS |
| Custom save-file update; default boot file remains unchanged | PASS |
| `skip_saving` changes active configuration without changing saved file | PASS |
| Real invalid commit reports failure and retains previous active configuration | PASS |
| Unwritable procfs save reports failure after successful active commit; saved file remains unchanged | PASS |
| A subsequent canonical plan can be a no-op after failed save | PASS; explicit persistence recovery is required |
| Recovery through a successful update/save | PASS |
| Native reload of provider-written custom saved bytes restores a contradictory active value | PASS |
| Authentication failure reports a diagnostic and retains Terraform state | PASS |
| Create whose save fails retains the committed resource ID in actual Terraform state | PASS |
| Destroy of that failed-create resource removes active/saved configuration | PASS |
| Native reload of provider-written default boot bytes removes an unsaved probe | PASS |
| Destroy cleans up all managed resources in active/saved configuration | PASS |
| Final three-test acceptance suite with Terraform 1.17.1 | PASS, all three executed (110.25s / 107.18s / 66.93s) |

Native inspection used `cli-shell-api` active configuration and independently
parsed saved files with VyOS `ConfigTree`. Custom API save files are root-owned
mode 0600. The native CLI account therefore loaded an owned copy, checked
byte-for-byte against the original before reload; file content was not modified.
Both custom and default saved files were tested against contradictory unsaved
active configuration.

## Fixture refinements and limits

The first combined acceptance run's 110-second harness limit was too short;
the final runner allows 15 minutes. A seed already contained the first proposed
SSH flag, so tests now use a confirmed-absent flag. VyOS creates missing save
directories, so a nonexistent directory was not a valid save-failure fixture;
procfs provides a deterministic unwritable target. The unprivileged native load
initially lacked permission to read a mode-0600 API save; the byte-identical owned
copy resolved that fixture constraint. Failed intermediate runs were retained in
local lab logs rather than counted as successful validations.

A fresh import records router JSON, not a previous user's representation.
Whitespace/null-member/singleton-list differences can require one reconciliation
apply. Immediate import no-op was verified with canonical complete JSON;
subsequent equivalent refreshes preserve the configured representation.

The VM boots with **`nopersistence`** and recreates its seed on cold boot. These
checks prove saved-file writes and explicit native reload, **not cold-reboot
persistence**. No production rollout, Cloudflare application deployment, real
Internet connectivity, whole-apply transaction, or commit-confirm rollback was
tested or implemented. A separate persistent VM is required for a reboot claim.

Temporary lab fixture cleanup: **PASS**. After final acceptance, native active and
saved checks confirmed all managed test resources were absent. The temporary
HTTPS API, PKI certificate, firewall sentinel, custom save files and test keys
were removed and the cleaned configuration saved. The pre-existing SSH security
flag remained present. The original isolated lab is still running and ready.
Production configuration and the containing repository's untracked `.gitignore`
and `lab/` work were preserved. No production rollout, GitHub issue/PR comments, merge, or release occurred
during validation.

Local evidence is retained outside the public provider checkout as
`provider-local-validation.log`, `provider-ci-acceptance.log`,
`provider-integration.log`, and `provider-integration-results.json`. Intermediate
fixture-refinement logs are retained separately. The review patch contains no
router configuration export or production credentials.
