# internal/redact

This package is an in-repo fork of `github.com/Azure/redact`.

## Source

- Upstream repository: https://github.com/Azure/redact
- Primary upstream file used: `redact.go`
- Upstream license: MIT (copied into [LICENSE](LICENSE))

The fork exists to allow small behavior changes that are not available in the upstream package.

## Why this was created

When using reflection-based redaction across our API models, values of external types like `*azcorearm.ResourceID` were being traversed and internal string fields were redacted. That mutated resource IDs (for example, replacing path segments like `subscriptions` and `resourceGroups` with `REDACTED`) and could produce invalid IDs. Because the problem types are external, the nonsecret tags could not be added.

## Local customization

This fork adds support for a `notraverse` tag option in addition to existing tag behavior.

- `redact:"nonsecret"`: preserve string fields.
- `redact:""` (or no recognized tag): redact string fields to `REDACTED`.
- `redact:"notraverse"`: do not recurse into nested fields of this value (and preserve the value itself).

## Scope

This package is intentionally small and focused. If upstream adds equivalent support in the future, we can re-evaluate whether to converge back to the external dependency.
