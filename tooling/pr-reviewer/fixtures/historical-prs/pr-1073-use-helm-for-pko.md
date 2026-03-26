# PR #1073 — Use Helm for PKO

- **Title:** `Use helm for pko`
- **Merged:** 2025-01-20
- **Touched areas:** `pko/helm/`, `pko/pipeline.yaml`, `config/config.yaml`, `config/config.schema.json`, `.github/workflows/services-ci.yml`

## Why it mattered

This PR moved PKO delivery into Helm and changed the operator's deployment, config, and runtime privilege shape. Reviewers treated it as more than a packaging migration because it affected image sourcing, RBAC scope, config ownership, and whether old build/pipeline glue was still hanging around after the move.

## High-signal review moments

- Reviewers asked why a custom PKO image was being built instead of mirroring or reusing an upstream image.
- Reviewers flagged that `config.mk` should not remain required once `pipeline.yaml` was the real deployment path, and they also caught an accidental commit.
- Reviewers pushed for MI names, namespaces, and service-account names to move into `config.yaml` parameters rather than staying implicit in chart files.
- Reviewers explicitly asked whether the chart could use a more fine-grained role, noting that the assigned service account kept that privilege during runtime.

## Reusable lesson

For lifecycle-operator Helm migrations, the reviewer should verify:

- whether custom image/build steps are really necessary
- whether obsolete build or pipeline glue remains after the new deployment path lands
- whether runtime identities, namespaces, and service accounts are parameterized in config rather than hidden in templates
- whether RBAC is minimized for the runtime service account instead of staying broad by default
