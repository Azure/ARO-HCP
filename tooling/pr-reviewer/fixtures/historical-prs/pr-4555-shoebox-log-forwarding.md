# PR #4555 — Shoebox log forwarding

- **Title:** `Feat shoebox`
- **Merged:** 2026-03-20
- **Touched areas:** `dev-infrastructure/` Helm fixtures, `observability/arobit/` templates, `test/e2e/kusto_logs_present.go`

## Why it mattered

The PR reworked how management-cluster logs were transformed and forwarded. This mixed infra templates, observability behavior, and E2E evidence.

## Review/rollout signals

- The author explicitly called out a related follow-up PR (`#4385`) for namespace/resourceId mapping instead of hiding the dependency.
- Approval context referenced several OWNERS scopes: `dev-infrastructure`, `observability`, and `test`.
- Rollout confidence was justified with prior E2E passes and urgency for INT.

## Reusable lesson

For observability + infra rollout PRs, the reviewer should ask:

- is cluster-type gating correct?
- were generated fixtures updated?
- is there enough runtime evidence for the operational risk?
- are known follow-up dependencies acknowledged rather than silently omitted?
