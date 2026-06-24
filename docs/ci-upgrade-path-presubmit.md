# Upgrade-Path Presubmit (`upgrade-e2e-parallel`)

An optional presubmit job that validates infrastructure upgrades from `main` to the PR branch. Unlike the standard `e2e-parallel` job which provisions a fresh environment, this job catches regressions that only appear when upgrading existing infrastructure (e.g., breaking Bicep parameter changes, missing migration steps, incompatible config transitions).

## When to use it

- Manually via `/test upgrade-e2e-parallel` on any PR
- Use it when your PR modifies infrastructure definitions and you want to verify the change is safe to apply on top of what's currently deployed from `main`

## How it works

The job uses the `aro-hcp-upgrade-e2e` workflow with these phases:

1. **`aro-hcp-provision-from-main`** — Checks out the `main` branch and provisions a baseline environment using main's Bicep templates, Helm charts, and config (no CI image overrides)
2. **`aro-hcp-upgrade-environment`** — Runs in a fresh container with the PR source (each step gets its own container), applies CI-built image overrides, and re-runs `make entrypoint/Region` against the already-provisioned environment (ARM idempotent upgrade)
3. **`aro-hcp-test-local`** — Runs the full e2e test suite against the upgraded environment
4. **Post steps** — Gathers artifacts and deprovisions the environment

## Interpreting failures

| Failed step | Meaning | Action |
|---|---|---|
| `aro-hcp-provision-from-main` | The `main` branch itself is broken — the baseline provision failed before the PR was applied | Check if `main`'s CI is green; this is not a PR issue |
| `aro-hcp-upgrade-environment` | **The PR introduces an upgrade-path regression** — the environment provisioned from `main` could not be upgraded to the PR's state | This is the signal this job is designed to catch. Investigate the Bicep/Helm/config diff between `main` and your PR |
| `aro-hcp-test-local` | The upgraded environment has a functional regression — e2e tests failed after the upgrade | Could be upgrade-related or a general bug in the PR's service code |

## Rehearsal expectations

When this job is rehearsed via Prow's `pj-rehearse` plugin on an `openshift/release` PR, it will attempt to run the full provision -> upgrade -> test -> deprovision cycle. Expect:
- Longer runtime than `e2e-parallel` due to the dual provision cycle, though the upgrade phase is faster than fresh provision since ARM skips unchanged resources
- Requires a Boskos lease from `aro-hcp-msi-mock-cs-sp-dev`
- Uses the same `ci01` environment and Azure subscriptions as the standard `e2e-parallel` job

## Relationship to `e2e-parallel`

`upgrade-e2e-parallel` is on-demand only - it runs when manually triggered via `/test upgrade-e2e-parallel`. `e2e-parallel` validates fresh provisioning while `upgrade-e2e-parallel` validates upgrading existing infrastructure. Both jobs acquire leases from the same Boskos pool (`aro-hcp-msi-mock-cs-sp-dev`) and the `aro-hcp-lease-acquire` step assigns each job a distinct environment slot, so they can run concurrently if pool capacity allows.

## Known limitations

- The `provision-from-main` step uses the PR's pre-built `templatize` binary (baked into the container image) with `main`'s config/Bicep/Helm files. In rare cases where a PR changes `templatize` in a way incompatible with `main`'s pipeline definitions, the baseline provision may fail spuriously.
