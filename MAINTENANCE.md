# Maintenance review, 4 November 2026

Fetched full public history and current main from
[benjamin-pilgrim/terraform-provider-vyos](https://github.com/benjamin-pilgrim/terraform-provider-vyos).
Main remains `53486a373df8f32f751865e3b5eaf0580dd622f9`.
Inspected the 53 issue/PR records and 46 PR metadata records, and discussion comments
for the functional issues below. No GitHub comments, replies, merges, or releases
were posted.

## Functional issues and historical fixes

| Issue/PR | Finding and patch |
| --- | --- |
| [#52](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/52), [#18](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/18), merged [#19](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/19) | Update bypassed the save wrapper introduced by #19. All mutations now use the same locked configure/save sequence, honoring custom files and skip-saving. |
| [#53](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/53) | Template output was JSON-encoded twice. README includes a JSON template example. |
| [#51](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/51) | Existing interface ownership requires import or narrower disjoint paths, not adoption. Documented. |
| [#16](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/16) | Duplicate subtree ownership causes drift; discussion also identifies singleton-list/scalar normalization. Documented ownership; reject ancestor/descendant overlaps in one instance and compare singleton lists semantically. |
| [#1](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/1) | Discussion confirms empty objects represent valueless nodes. Null object members are omitted; top-level JSON null and ambiguous empty values are rejected. |
| merged [#4](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/4) | Indefinite config cache concealed external changes and raced invalidation. Removed cache; serialized fresh reads and mutation/save sequences. |
| [#20](https://github.com/benjamin-pilgrim/terraform-provider-vyos/issues/20) | Historical concurrent API segfault reportedly fixed upstream. Preserve serialization; fail closed on HTTP/non-JSON/API failures without exposing response bodies. |
| merged [#35](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/35) | Preserve RemoveResource only for confirmed absence. Retrieval failures cannot silently remove state. |

Additional repairs: secure TLS defaults with additive CA support and explicit
insecure override, sensitive resource values, no secret-bearing provider logs,
path/value validation before any destructive request, and deterministic failure tests.

The pinned VM's `/usr/libexec/vyos/services/api/rest/models.py` defines
`BaseConfigureModel.value: StrictStr = None`. The separate `value` field is valid;
the patch preserves it. Runtime acceptance exercises that field directly.

## Dependency overlap

All nine open PRs were dependency updates, with no overlapping functional patch:
[#38](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/38) (checkout),
[#42](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/42) (GoReleaser action),
[#43](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/43) (GPG action),
[#45](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/45) (x/net),
[#46](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/46) (Framework),
[#47](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/47) (gRPC),
[#48](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/48) (Terraform action),
[#49](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/49) (SDK), and
[#50](https://github.com/benjamin-pilgrim/terraform-provider-vyos/pull/50) (plugin-go).
Their 2023 version targets are superseded here rather than merged individually.
Closed dependency PRs include superseded proposals; merged #32–34 establish the
old dependency baseline.

The Go proxy's current published module versions and official release metadata
were checked before upgrading:

| Component | Previous | New |
| --- | --- | --- |
| Terraform Plugin Framework | 1.3.5 | 1.19.0 |
| Plugin protocol (`terraform-plugin-go`) | 0.18.0 | 0.31.0 |
| Plugin Testing | old SDK helper | 1.16.0 |
| Plugin Docs | 0.16.0 | 0.25.0 |
| Plugin Log (indirect) | 0.9.0 | 0.11.0 |
| SDK v2 (indirect test/tool dependency) | 2.26.1 direct | 2.40.1 indirect |
| gRPC | 1.56.1 | 1.85.0 |
| x/net | 0.11.0 | 0.62.0 |
| foltik/vyos-client-go | 0.4.2 | removed |

The first vulnerability scan found nine reachable vulnerabilities in the older
networking/gRPC dependency minimums. Explicit module upgrades to current releases
resolved them; the final scan reports no vulnerabilities.

The upstream VyOS client has no newer published version and does not check HTTP
status or `success:false`. A small internal HTTP client replaces it and its Resty
dependency. Current Plugin Testing still pulls SDK v2 indirectly; this provider's
implementation stays on Framework. CI uses the tested Go 1.27 toolchain series.
The module now requires Go 1.26.0, following the current networking/security
dependencies' minimum. CI actions are upgraded to current published releases, including checkout 7.0.1, setup-go 7.0.0 and setup-terraform 4.0.1.

HashiCorp recommends Framework for new development and migration where possible:
[Framework benefits](https://developer.hashicorp.com/terraform/plugin/framework-benefits).
[Framework 1.19.0](https://github.com/hashicorp/terraform-plugin-framework/releases/tag/v1.19.0)
is the latest stable release checked, although the documentation selector still
labels 1.18.x as latest. This is a dependency upgrade, not a migration to SDK v2.

Real VM acceptance is explicitly dispatched against a prepared pinned isolated
runner. The former mutable privileged Docker image and Terraform 1.0–1.2 matrix
are removed. Integration evidence uses Terraform 1.17.0; final acceptance and CI
select 1.17.1. CI itself has not been executed on GitHub as part of this local patch.
