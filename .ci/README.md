# Automated VyOS acceptance tests

Run this on Linux:

```sh
.ci/acceptance.sh
```

The command does the setup itself. It resolves the latest official VyOS rolling
release to an exact dated version and GitHub-published SHA256, downloads and
verifies that ISO, boots a disposable QEMU VM, creates a temporary HTTPS
certificate and API key, and runs the
Terraform acceptance suite. Native console checks compare active and saved
configuration after creates and updates, including batches. A connection-loss
test leaves a deletion unconfirmed and unsaved, waits for actual rollback, then
retries destroy. The test VM uses the reload commit-confirm action.
Tests verify destroy cleanup plus an unrelated sentinel. The VM is stopped and
its temporary files removed on success,
failure, or cancellation.

No existing router, production configuration, API key, repository secret,
self-hosted runner or separate lab setup is needed. Tests use official VyOS
images. Each run records its exact build and checksum in
`build.json` and `result.json`. The version is resolved once per run; a new release
published during a test cannot change its image or expected API version.

## Requirements

Go >= 1.26, Terraform (CI uses 1.17.1), Python >= 3.11, QEMU x86, xorriso and
OpenSSL, with hardware virtualization available through a readable/writable
`/dev/kvm`. On Ubuntu, install the VM tools with:

```sh
sudo apt-get update
sudo apt-get install -y --no-install-recommends qemu-system-x86 xorriso openssl
```

The VM runs without root and requires KVM. Its network is isolated; tests connect
to its HTTPS API through localhost. The runner independently verifies the
bootstrap HTTPS certificate and exact API version.

The host needs Internet access for release metadata, the ISO and dependencies. The guest has no
Internet requirement. ISO bytes are verified even after a cache hit; boot files
are extracted afresh from that verified ISO. Download failure, checksum mismatch,
wrong API version, failed native checks or failed Terraform tests fail the job.

## GitHub CI

The `VyOS rolling acceptance` workflow runs automatically for pull requests targeting `main` or the maintenance
stack, and pushes to `main`, and can also be started manually. It uses a standard
`ubuntu-24.04` GitHub-hosted runner, installs its own tools and caches the ISO by
checksum. It uploads diagnostic logs and a result JSON on success or failure.
No custom runner label, environment, credentials or wrapper needs provisioning.

The job allows 40 minutes, including download/build and VM boot. The guest boot
timeout is 10 minutes and the acceptance suite timeout is 20 minutes. Diagnostic output is under `.ci-results/`; private
seed/certificate files are kept separately in a temporary directory.

Local overrides:

- `TF_ACC_TERRAFORM_PATH`: an existing Terraform executable.
- `VYOS_CI_CACHE`: ISO cache directory (default `.ci-cache`).
- `VYOS_CI_OUTPUT`: diagnostic directory (default `.ci-results`).
- `VYOS_CI_RELEASE`: `latest` (default), or an exact dated rolling tag to replay.
- `VYOS_CI_BUILD`: a recorded `build.json` to replay its exact version and checksum,
  without resolving release metadata again. CI passes its already resolved manifest.

These overrides do not select an existing router. The runner always creates its
own endpoint and test credentials, overriding inherited provider settings.
For advanced use, `go test` with `TF_ACC=1` can still target an existing disposable
lab directly; that bypasses this VM setup and requires the usual provider settings.

Run the runner's release-resolution/checksum/isolation unit tests with:

```sh
python3 -m unittest discover -s .ci -p 'test_*.py' -v
```

The ISO boots live with `nopersistence`. Active/saved checks establish save-file
behavior within the running VM; they do not establish cold-reboot persistence.
