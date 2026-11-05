# Terraform Provider VyOS

A Terraform Plugin Framework provider for the VyOS HTTPS API. CI tests the latest
official VyOS rolling release and records the exact build and checksum for each
run. Build with Go >= 1.26.0. Acceptance tests and CI use Terraform 1.17.1.

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
  value = {
    "default-action" = "drop"
    rule = {
      "10" = { action = "accept", protocol = "tcp", destination = { port = "443" } }
    }
  }
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
individual path components (so object keys can include spaces). Values accept native
HCL objects, lists and scalar strings, or existing JSON strings produced by
`jsonencode`. A string that contains valid supported JSON keeps its legacy JSON
meaning; wrap it with `jsonencode` if you need that text as a literal scalar:

- Strings configure scalar leaves. Use strings for ports, numbers and booleans.
- Nonempty lists of strings configure multivalue leaves. Single-element lists and
  their scalar form compare equally on refresh because VyOS may return either.
- `{}` configures a valueless flag; omitted or null resource `value` defaults to `{}`.
- Null **object members** are omitted. Empty lists, nonstring list members,
  and objects with no effective values are rejected.
- Whitespace and object-key order are ignored on refresh. Multivalue list order is
  significant; match the order returned by your router. No schema-wide coercion
  of numeric/bool values or router-generated defaults is attempted.

To manage existing VyOS settings with Terraform, import their path:

```sh
terraform import vyos_config.ingress_rules 'firewall ipv4 name TUNNEL-INGRESS'
```

Then set `value` to all settings you want to keep at that path. Run
`terraform plan` and review the changes before applying. Settings left out of
`value` will be removed when you apply.

## Batch related changes

Use `vyos_config_batch` to manage several disjoint paths with one commit and save:

```hcl
resource "vyos_config_batch" "policies" {
  entries = {
    ingress = {
      path  = ["firewall", "ipv4", "name", "APP-INGRESS"]
      value = { "default-action" = "drop" }
    }
    egress = {
      path  = ["firewall", "ipv4", "name", "APP-EGRESS"]
      value = { "default-action" = "accept" }
    }
  }
}
```

Each entry owns its complete subtree. Paths must not overlap other entries or
resources. Value changes update the batch; changing entry names or paths replaces
it. Provider saving settings also apply to batches. Use `depends_on` between
batches when one needs settings created by another.
VyOS can partially apply a failed request across components. A failed batch
create retains its identity so Terraform can clean up and retry.

See [the batch resource](docs/resources/config_batch.md) for import syntax.

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

Set `commit_confirm_minutes = 2` in the provider to enable a rollback timer for
each create, update and destroy. The provider checks a fresh API connection and the changed
configuration, confirms, verifies again, then saves. If the connection drops or
verification fails, the operation fails without saving. VyOS can recover when
the timer expires. Failed timed changes retain state and block early retries
until the recovery window has passed. This checks router access and configuration, not network
traffic, and does not make the whole Terraform apply atomic.

The default is `0` (disabled). VyOS defaults to rebooting into the saved
configuration on expiry; `system config-management commit-confirm action reload`
selects recovery without reboot. A confirmed change can still fail to save;
restore saving and save explicitly on the router. Lost responses leave the
result uncertain; inspect the router before retrying. Mutations are not retried
automatically.

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

Run `.ci/acceptance.sh` to automatically boot a disposable VM of the latest rolling build and run real
acceptance tests; see [.ci/README.md](.ci/README.md). GitHub CI runs the same command
on a standard hosted runner, without external lab setup or secrets.
The regular suite uses deterministic fake HTTP/TLS servers and covers persistence,
HTTP/API errors, drift refresh, concurrency, input validation and failure state.
