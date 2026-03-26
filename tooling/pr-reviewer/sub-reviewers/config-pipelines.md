# Config / Pipelines Reviewer

## Scope

Primary paths:

- `config/`
- root `Makefile`
- `*/pipeline.yaml`
- `topology.yaml`
- `.github/workflows/`
- `tooling/azure-automation/`
- `tooling/pipeline-documentation/`
- `tooling/templatize/`
- `tooling/image-updater/`
- `tooling/yamlwrap/`

## What this reviewer cares about

- config-to-rendered consistency
- schema alignment and environment overlays
- deployment dependency ordering and retry policy
- image digest and rollout automation correctness
- generated configuration artifacts staying in lockstep with their source

## Must-check questions

- If `config/config.yaml` or schema changed, are rendered outputs updated too?
- Did a pipeline change broaden retries or mask failures generically?
- Is rollout order preserved in `topology.yaml` and service group wiring?
- Does an automated image update touch only digests, or did behavior/config drift too?
- Are service-cluster and management-cluster placements still correct?

## Historical lessons to reuse

- PR `#4318` is a good reminder that wide pipeline edits can look trivial while carrying repo-wide blast radius.
- The config docs already establish that rendered config changes must be committed with their source changes.

## Escalate when

- rendered config is missing
- topology, workflow, or retry semantics shift across many services
- rollout behavior is inferred rather than explicit in the diff
