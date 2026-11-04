# Real VyOS acceptance tests

Use a disposable, isolated VM pinned to **2026.10.22-0026-rolling**.
The former `foltik/vyos:rolling-latest` privileged Docker test has been removed:
it did not establish compatibility with a reproducible VyOS build.

Bootstrap HTTPS with the current `service https api rest`,
`service https api keys id provider-test key ...`, `service https listen-address ...`,
and `service https certificates certificate ...` syntax. Import a test-only
certificate/key into `pki certificate`; the certificate must have a SAN matching
the endpoint. Never use a production configuration or API key for tests.

The checked-in config.boot is a minimal seed, not a complete network/PKI setup.
Do not boot or test it against a production-connected NIC.

On a prepared host runner, set `VYOS_ENDPOINT`, `VYOS_API_KEY`,
`VYOS_CA_CERTIFICATE_FILE`, and `VYOS_LAB_EXEC`, then run `.ci/acceptance.sh`.
`VYOS_LAB_EXEC` is the absolute path of a trusted executable wrapper that enters
only the isolated client network namespace and executes its arguments. It must
preserve the working directory and these environment variables, and share the
host filesystem paths containing the prebuilt binaries and CA bundle.

The script builds the test binary on the host, then calls the wrapper to check
the pinned VM version and execute the binary in the offline network.
`TF_ACC_TERRAFORM_PATH` can point to a preinstalled Terraform executable. For
manual use, prebuild with `go test -c -o /tmp/provider-tests ./internal/provider`
and run `.ci/acceptance.sh --inside-lab /tmp/provider-tests` from inside the
isolated namespace with the endpoint/key/CA/Terraform environment set.

Ordinary CI runs deterministic fake-server and race tests. Real acceptance is a
separate manually dispatched workflow targeting a prepared `vyos-isolated-lab`
self-hosted runner and environment. The host runner needs GitHub access, while its wrapper isolates the test process
with a disposable pinned VM and no production connection. Endpoint/CA/wrapper
paths are environment variables;
the test key is an environment secret. A hosted GitHub runner cannot reach this
local lab. Acceptance does not silently skip when its required endpoint is absent.

Tests use `firewall ipv4 name TF-PROVIDER-TEST` and the SSH host-validation
flag; these must be initially absent. They create, import, update, and destroy
configuration. Native active/saved checks and explicit saved-file reload are
additional integration checks; see VALIDATION.md for this patch's actual evidence.
A live VM booted with `nopersistence` does not prove cold-reboot persistence.
