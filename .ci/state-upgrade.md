# Native-value state migration

The single and batch resources use schema version 1. Existing JSON strings,
path components and resource IDs are preserved. Native HCL is optional;
existing `jsonencode` configuration can remain unchanged.

Before changing provider version, stop other writers and use `terraform state pull`
to save an access-controlled state snapshot. State contains configuration values
and credentials. Record the previous provider version and lock file with it.
Run `terraform plan` with the new provider before applying. Unchanged legacy
configuration should produce no router mutations or resource replacements.

Applying or refreshing with the new provider may persist schema-v1 state. An
older provider rejects that state as managed by a newer provider; reverting the
binary alone does not downgrade it. Recovery requires retaining the newer
provider, or recovering the pre-upgrade snapshot together with the old provider.
Reconcile current router configuration and coordinate backend locking and other
writers before restoring a snapshot. State recovery does not revert router
changes made after the backup.

CI runs scalar, omitted-flag and batch scenarios in `.ci/check-state-upgrade.py` with the real Terraform CLI and an isolated
HTTPS fixture. It builds production source from main revision `53486a3` and batch schema-v0 revision `7b47778` using
the current dependency graph, creates real schema-v0 state, upgrades to the
current provider, verifies no-op plans and stable identity with zero router
writes, and confirms that the old provider rejects schema-v1 state.

An unchanged batch may initially normalize map/list state into the dynamic
object/tuple representation of its HCL. CI requires stable identity, an in-place
state-only update with zero router writes, and a subsequent no-op plan. Single
resources, including omitted flag defaults, require an immediate no-op plan.
