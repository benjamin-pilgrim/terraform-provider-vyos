## Unreleased

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
- Replace mutable container acceptance CI with automatic disposable-VM
  acceptance against the latest official rolling release, recording its exact build and checksum on GitHub-hosted runners, update firewall/HTTPS examples, and add deterministic HTTP/TLS,
  persistence, concurrency and failure-state tests.

Resources retain exclusive subtree ownership. No whole-apply transaction,
commit-confirm rollback, or automatic recovery from save failure is provided.
See README.md and VALIDATION.md for scope and migration details.
