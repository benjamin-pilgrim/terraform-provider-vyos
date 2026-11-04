# Terraform Provider VyOS

A Terraform Plugin Framework provider for the VyOS HTTPS API. This patch is
validated against **VyOS 2026.10.22-0026-rolling**; other versions need their own
acceptance run. Build with Go >= 1.26.0. The integration CLI is Terraform 1.17.0; final acceptance and CI target 1.17.1.

```hcl
terraform {
  required_providers {
    vyos = { source = "TGNThump/vyos" }
  }
}

provider "vyos" {
  endpoint            = "https://router.example.net"
  ca_certificate_file = "/path/to/router-ca.pem"
  # Supply VYOS_API_KEY through your execution environment.
}

resource "vyos_config" "ingress_rules" {
  path = "firewall ipv4 name TUNNEL-INGRESS"
  value = jsonencode({
    "default-action" = "drop"
    rule = {
      "10" = { action = "accept", protocol = "tcp", destination = { port = "443" } }
    }
  })
}
```

## Ownership and values

Each resource **exclusively owns its entire selected subtree**. Updates delete and
rebuild that subtree in one configure request; destroy deletes it. Unrelated
siblings outside the selected path remain untouched. Anything beneath the path
that is absent from the desired value is removed on update. Import an existing
subtree before managing it, including all settings you want to keep, or select a
narrower path such as one interface description or one firewall rule.

Resource paths must be unique and disjoint, including across modules, states,
provider aliases, and external automation. The provider rejects ancestor/descendant
overlap observed within a single configured instance. It cannot detect duplicate
Terraform addresses owning the exact same path, or coordinate independent provider
instances/external writers. `depends_on` orders actions but does not make overlapping
ownership safe. Avoid `create_before_destroy` for an unchanged path.

Paths are nonempty, with a single space between components. Object keys are
individual path components (so tag names inside JSON can include spaces). Values
use the JSON shape returned by the VyOS API:

- Strings configure scalar leaves. Use strings for ports, numbers and booleans.
- Nonempty lists of strings configure multivalue leaves. Single-element lists and
  their scalar form compare equally on refresh because VyOS may return either.
- `{}` configures a valueless flag; omitted or null resource `value` defaults to `{}`.
- Null **object members** are omitted. A top-level JSON `null`, empty lists,
  nonstring list members, and objects with no effective values are rejected.
- Whitespace and object-key order are ignored on refresh. Multivalue list order is
  significant; match the order returned by your router. No schema-wide coercion
  of numeric/bool values or router-generated defaults is attempted.

For issue #53's template use case, pass a template that produces valid JSON
straight to `value`. Do not JSON-encode a template's JSON text a second time:

```hcl
value = templatefile("${path.module}/ntp.json.tftpl", {
  ntp_servers = ["time1.google.com", "time2.google.com"]
})
```

```hcl
# ntp.json.tftpl
${jsonencode({ for server in ntp_servers : server => {} })}
```

Import with `terraform import vyos_config.ingress_rules 'firewall ipv4 name TUNNEL-INGRESS'`.
After import, set `value` to the complete desired subtree before applying. For an
immediate no-op, use `jsonencode` with the router's exact JSON shape. Import has no
prior user value to preserve: alternate JSON whitespace, omitted-null members,
or scalar/singleton-list representations can require one reconciliation apply
before subsequent plans become no-ops. Refresh preserves equivalent formatting
once that desired value has been recorded in state.

## Persistence and operational limits

Create, update, and delete commit per resource and then save the **whole active
router configuration** by default. `save_file`/`VYOS_SAVE_FILE` selects a different
remote save file; it does not change which file the router boots.
`skip_saving`/`VYOS_SKIP_SAVING=true` leaves changes only in the active configuration.
A save also persists unrelated changes already active on the router. Changing
provider save settings alone does not issue a save: a no-op apply makes no mutation.
Use an explicit router save to persist changes already active.

Configure and save calls are serialized within one provider instance. Reads fetch
fresh active configuration every time; retrieval/authentication/transport errors
produce diagnostics and retain state. A path is removed from state only after a
successful read proves it absent. Existing configuration is never silently adopted
on create, including existing flags.

There is **no whole-apply transaction, commit-confirm timer, automatic rollback,
or protection against losing API reachability**. Terraform may finish some resource
commits before another fails. A commit can succeed and its subsequent save fail;
the diagnostic distinguishes that case and a failed create retains its identity.
Restore saving and save explicitly on the router: a later Terraform apply can be a
no-op because the active configuration already matches. A transport timeout leaves
the commit result uncertain; inspect active/saved configuration before retrying.
A lost save response also leaves persistence uncertain; inspect the saved file.
There are no automatic mutation retries.

For firewall, interface, routing, API or access-control changes, validate in an
isolated lab first. Keep working console/out-of-band access and stage changes so
the API remains reachable. Use a separate native VyOS commit-confirm workflow when
rollback on lost management access is required; this provider does not provide it.

## TLS and secrets

HTTPS certificate and hostname verification is enabled by default using system
roots. `ca_certificate_file`/`VYOS_CA_CERTIFICATE_FILE` adds a PEM CA bundle. The
explicit `insecure=true`/`VYOS_INSECURE=true` override is for isolated development.
Cleartext endpoints and redirects are rejected, protecting the API key.

The API key and resource value are sensitive in Terraform output. **Sensitive
values are still stored in state and saved plan files**; use access-controlled,
encrypted state storage. Moving from older releases marks existing values
sensitive but does not remove secrets from old state versions or logs. The
provider does not log configuration values/payloads or echo raw router error
bodies; diagnostics refer to router logs for details. Paths themselves are
identifiers, so do not put secrets in path components.

## Development and validation

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
go generate ./...
```

Real tests require `TF_ACC=1` and a disposable router; see [.ci/README.md](.ci/README.md).
The regular suite uses deterministic fake HTTP/TLS servers and covers persistence,
HTTP/API errors, drift refresh, concurrency, input validation and failure state.
See [VALIDATION.md](VALIDATION.md) for actual lab evidence and
[MAINTENANCE.md](MAINTENANCE.md) for issue/PR overlap and dependency decisions.
