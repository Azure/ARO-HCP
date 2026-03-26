# PR #1633 — Deploy self managed Prometheus

- **Title:** `deploy self managed prometheus`
- **Merged:** 2025-04-24
- **Touched areas:** service and pod monitors across many components, `config/config.yaml`, public-cloud overlays, `dev-infrastructure/` metrics and cluster templates, `observability/prometheus/`, `docs/monitoring.md`

## Why it mattered

This PR rolled out self-managed Prometheus across a wide set of services and deployment surfaces. Reviewers treated it as an operational systems change: if the monitors, infrastructure, persistence, anti-affinity, and docs did not line up, the repo would ship a monitoring stack that was present on paper but fragile or misleading in practice.

## High-signal review moments

- Reviewers asked how the newly deployed Prometheus instance would itself be monitored and whether the selected monitors were actually useful in the target environment.
- Good comments challenged persistence and anti-affinity choices instead of assuming the default Helm shape was operationally sufficient.
- Review discussion also pushed for deployment docs and workload-identity/remote-write references to match the implementation rather than leaving the rollout justified only by tribal knowledge.

## Reusable lesson

For broad observability rollout PRs, the reviewer should check:

- whether the monitoring stack itself has a health story
- whether persistence, anti-affinity, and rollout topology are resilient enough for the intended environment
- whether every new monitor or scrape target has a clear operational purpose
- whether config, infrastructure templates, and docs describe the same runtime system
