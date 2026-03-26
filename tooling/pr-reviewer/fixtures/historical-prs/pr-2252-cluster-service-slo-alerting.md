# PR #2252 — Cluster Service SLO dashboards and alerting rules

- **Title:** `Add Cluster Service SLO dashboard and alerting rules`
- **Merged:** 2025-08-13
- **Touched areas:** `cluster-service/grafana-dashboards/`, `observability/alerts/tested-rules/`, `observability/observability*.yaml`, `dev-infrastructure/modules/metrics/rules/`, `tooling/prometheus-rules/`

## Why it mattered

This PR changed how Cluster Service health and reliability were measured and alerted on. That made it more than an observability cosmetics change: the dashboard text, PromQL, generated rules, tested-rule fixtures, and alert severity all had to describe the same behavior or operators would end up trusting the wrong SLO.

## High-signal review moments

- Reviewers caught a mismatch between a `99.9%` target and the intended `99%` target across dashboards and rule logic.
- Reviewers noticed the written SLO definition mentioned timeout behavior that the query itself did not actually capture.
- Reviewers pushed for the tested-rule layout because the generator and resulting Bicep outputs could be wrong if tested and untested rules were mixed together.
- Reviewers also enforced the sev-3 alert limit rather than accepting more alerts simply because the rules compiled.

## Reusable lesson

For cluster-service observability and SLO PRs, the reviewer should verify:

- the written SLO definition, PromQL, and threshold math all agree
- generated rule outputs, tested-rule fixtures, and dashboards move together
- alert severity budgets stay within the repo's operational policy
- service-specific observability changes are treated as behavioral risk, not just visualization work
