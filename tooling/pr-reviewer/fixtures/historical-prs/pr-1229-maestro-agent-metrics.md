# PR #1229 — Maestro agent metrics

- **Title:** `add maestro agent metrics`
- **Merged:** 2025-02-04
- **Touched areas:** `maestro/agent/helm/`, `maestro/agent/pipeline.yaml`, `config/config.yaml`, `config/config.schema.json`

## Why it mattered

The PR added metrics exposure for the maestro agent, including Helm templates, a PodMonitor, metrics-proxy config, and config-driven rollout values. Reviewers focused on whether the observability gain was being achieved with the smallest possible operational surface area.

## High-signal review moments

- Reviewers pushed to reference the upstream/MCR image directly instead of mirroring it into ACR when no extra control value was being gained.
- Reviewers asked for errors to be logged to `stderr` so the cluster could surface them, rather than writing them into opaque files.
- Reviewers also pointed out that once logging moved to `stderr`/`/dev/null`, extra log-file volumes and related plumbing were no longer justified.

## Reusable lesson

For maestro agent observability PRs, the reviewer should check:

- whether image sourcing is simpler if upstream images can be referenced directly
- whether logs and metrics improve runtime visibility instead of hiding failures in file-based plumbing
- whether added volumes, secrets, or config are truly required or only support unnecessary indirection
- whether the rollout keeps the metrics path simple enough to operate and debug
