## Unreleased

- Add explicit bearer-token, mTLS and API-key token-exchange authentication modes.
  Preserve API-key defaults, validate credential conflicts, refresh exchanged tokens
  before expiry, and retain authentication on fresh commit-confirm connections.
  Verify server TLS by default; cap mTLS at TLS 1.2 for current VyOS compatibility.

- Add batch `include` entries for selected list values. Preserve unrelated values,
  detect owned-value drift, reject overlapping ownership, and retain attempted
  includes for cleanup after failed updates.

- Accept native HCL configuration values while retaining legacy JSON strings and
  upgrading existing resource state.
- Add optional `commit_confirm_minutes` for create, update and destroy. Verify
  router access and configuration before confirmation and saving; leave failed
  operations unsaved for router timeout recovery.

- Add `vyos_config_batch` for named, disjoint path arrays with one configure/save
  sequence, import support, drift refresh and failed-create cleanup identity.

- Fix update persistence: create, update, and delete now share serialized
  configure/save handling, including `save_file` and `skip_saving`.
- Refresh active configuration on every read, detect external drift, and retain
  Terraform state on API errors. Prevent competing creates and observed
  ancestor/descendant resource ownership within a provider instance.
- Reject HTTP, API-success, response-format and root-retrieval failures. Remove
  the unchecked upstream VyOS HTTP client and secret-bearing diagnostics/logs.
- **Security compatibility change:** verify HTTPS certificates/hostnames by
  default. Add `ca_certificate_file` and the explicit development-only `insecure`
  option. Reject cleartext endpoints and redirects.
- Mark resource values sensitive. Existing state/history still requires protection.
- Validate nonempty paths and JSON values before mutation; preserve flag semantics,
  omit null object members, and compare singleton lists/scalars on refresh.
- Upgrade Terraform Plugin Framework to 1.19.0 and Plugin Testing to 1.16.0,
  update networking/security dependencies, and require Go 1.26.0 or newer.
- Run automatic disposable-VM
  acceptance against the latest official rolling release, recording its exact build and checksum on GitHub-hosted runners, update firewall/HTTPS examples, and add deterministic HTTP/TLS,
  persistence, concurrency and failure-state tests.

Whole-value entries retain exclusive subtree ownership; include entries own only
their selected list values. Commit-confirm is per resource or
batch; no whole-apply transaction or automatic recovery from save failure is provided.
See README.md for scope and migration details.
