# CI Operations

This document is the operator and maintainer view of ARO HCP CI. Use it when you need to trigger jobs, inspect a failing run, correlate EV2 with Prow, or change the underlying CI configuration in `openshift/release`.

For the execution model and cross-tenant request flow, start with [CI Execution](execution.md). For contributor-facing E2E usage, see [E2E Testing In CI](e2e-testing.md).

## Triggering Jobs

### Pull-Request Commands

ARO HCP E2E jobs can be triggered or retriggered from PR comments in the `Azure/ARO-HCP` repository:

```text
/test e2e-parallel
/test integration-e2e-parallel
/test stage-e2e-parallel
/test prod-e2e-parallel
```

To rerun failed jobs:

```text
/retest
```

To rerun a specific job:

```text
/test <job-trigger>
```

### What Runs Automatically

- `images` and `frontend-simulation` are required presubmit signals.
- `e2e-parallel` in DEV always runs and is required.
- `integration-e2e-parallel`, `stage-e2e-parallel`, and `prod-e2e-parallel` are opt-in presubmits.
- EV2 gating jobs are not triggered from GitHub comments; they are started programmatically through Gangway as part of the EV2 pipeline.

## Inspecting Runs

The normal observation path is:

1. check the PR's **Checks** tab for presubmit status
2. open the job run in the [Prow dashboard](https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP)
3. inspect logs and artifacts for the failing step
4. correlate the failure with the target environment and execution mode

Useful signals include:

- job history in the Prow dashboard
- artifacts from the `aro-hcp-tests` run
- GitHub checks status on the PR
- Slack notifications in [`#forum-ocp-testplatform`](https://redhat.enterprise.slack.com/archives/CBN38N3MW) for build-farm-side failures and repeated issues

## Understanding EV2-Triggered Runs

EV2-triggered ARO HCP E2E runs look like postsubmit jobs because they are started with a pinned `base_sha`. To understand which rollout a Prow run belongs to:

For the full EV2-to-Prow wiring, Gangway auth path, and commit-pinning model, see [CI EV2 Integration](ev2-integration.md).

1. open the job in the Prow dashboard
2. inspect the annotations for keys prefixed with `ev2.rollout/`
3. use those annotations to map back to the rollout identifier, build number, region, and SDP pipeline revision

Common annotation fields include:

- `ev2.rollout/ARO-HCP`
- `ev2.rollout/build`
- `ev2.rollout/region`
- `ev2.rollout/sdp-pipelines`

If you are troubleshooting why a rollout did or did not gate promotion, this metadata is the quickest path from a Prow run back to the EV2 rollout that spawned it.

## Modifying CI Configuration

ARO HCP Prow job definitions are maintained in `openshift/release`, not in this repository. The generated job manifests under `ci-operator/jobs/Azure/ARO-HCP/` are outputs and should not be edited directly.

The main source-of-truth files are:

- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-cleanup.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__image-updater.yaml`
- step-registry workflows and refs under `ci-operator/step-registry/aro-hcp/`

When you change CI configuration in `openshift/release`, follow the release-repo regeneration workflow rather than hand-editing generated YAML. In practice that means using the repo's documented `make update` flow so ci-operator config, Prow jobs, and related generated artifacts stay in sync.

Also keep the ARO HCP-side wiring in mind:

- `config/config.msft.clouds-overlay.yaml` maps public-cloud environments to `prowJobName`
- `test/e2e-pipeline.yaml` passes `PROW_JOB_NAME` to EV2 gating

If one side changes without the other, the rollout path can drift even when the individual YAML files still look valid.

For the CI image graph itself, see [CI Image Lifecycle](image-lifecycle.md). The most important source-of-truth files for image behavior are:

- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__baseimage-generator.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml`
- `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__image-updater.yaml`
- `ci-operator/step-registry/aro-hcp/images-push/aro-hcp-images-push-ref.yaml`

## Maintaining Managed Identity Pools

The managed-identity container pool is a CI scaling control, not just a test implementation detail.

For the deeper lease lifecycle, runtime contract, and pool-sizing model, see [CI Identity Leasing](identity-leasing.md).

Operationally, the important tasks are:

- keeping the Boskos resource counts aligned with the number of identity-container resource groups that actually exist in Azure
- reapplying the identity pool when pool sizing or identity definitions change
- checking lease-related failures when jobs report missing or exhausted identity containers

The `identity-pool` subcommand in `test/cmd/aro-hcp-tests/identity-pool/` applies and maintains those identity-container resource groups. A typical invocation is:

```bash
./test/aro-hcp-tests identity-pool apply --environment dev
```

or, when you need a different pool size:

```bash
./test/aro-hcp-tests identity-pool apply --environment int --pool-size 150
```

When you change either the Boskos counts or the pool sizing assumptions, do both of the following:

1. regenerate the Boskos configuration in `openshift/release`
2. reapply the identity pool in every affected subscription

Common lease-related failures:

- **`expected envvar LEASED_MSI_CONTAINERS to not be empty`** usually means the job did not successfully request or receive Boskos leases
- **`no assigned identity containers available for <specID>`** usually means the test reserved fewer containers than it later attempted to consume
- repeated leftover FIC or role-assignment leakage in container resource groups usually points to permission issues or unexpected extra resources

## Maintaining MSI Mock Service Principal Pools

DEV local E2E also uses a Boskos-backed pool of MSI mock service principals so parallel jobs do not all share the same ARM throttle budget.

For the full pool rationale and the current release-side consumption path, see [CI Identity Leasing](identity-leasing.md#msi-mock-service-principal-pool).

The main lifecycle is:

1. create or recreate the pool infrastructure from `dev-infrastructure/`
2. grant the pool service principals access to the E2E test subscription
3. repopulate `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`
4. keep the Boskos resource type count and workflow lease count aligned

Typical commands:

```bash
cd dev-infrastructure/
make create-msi-mock-pool
make grant-msi-mock-pool-e2e-access
make populate-msi-mock-pool
```

When the pool shape changes, update both:

- `openshift/release: core-services/prow/02_config/generate-boskos.py`
- `openshift/release: ci-operator/step-registry/aro-hcp/local-e2e/aro-hcp-local-e2e-workflow.yaml`

The provisioning step then consumes `LEASED_MSI_MOCK_SP` and resolves the matching client ID, principal ID, and certificate name from `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`.

## Troubleshooting

### Job Stuck Pending

- check for general OpenShift CI load or incidents first
- verify the job is landing on the expected build-farm cluster
- if the problem is widespread or unrelated to ARO HCP configuration, escalate through the OpenShift CI team

### Test Failures In E2E Jobs

- first identify which execution mode failed: DEV PR, higher-environment PR, EV2 gating, or periodic
- confirm whether the failure looks like product behavior, environment drift, lease exhaustion, or test flake
- use [CI Execution](execution.md) to confirm what that specific job could realistically validate

### Cleanup Failures

- distinguish strict per-test cleanup from periodic cleanup before interpreting the signal
- review [CI Cleanup](cleanup.md) to understand whether the failure is supposed to fail loudly or be best-effort
- check for deletion locks, deny assignments, or missing owner components before assuming the cleanup code is wrong

### Getting Help

- build-farm or Prow infrastructure issues: [#forum-ocp-testplatform](https://redhat.enterprise.slack.com/archives/CBN38N3MW)
- ARO HCP-specific test failures: work through the ARO HCP development or SRE owners for the affected component
- CI config changes: submit a PR to `openshift/release` and involve the OpenShift CI reviewers as needed

## Tiny Appendix: Key Job Families And Source Of Truth

- **PR build and simulation**: `pull-ci-Azure-ARO-HCP-main-images`, `pull-ci-Azure-ARO-HCP-main-frontend-simulation` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **DEV PR E2E**: `pull-ci-Azure-ARO-HCP-main-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` and `ci-operator/step-registry/aro-hcp/local-e2e/`
- **Higher-environment PR E2E**: `integration-e2e-parallel`, `stage-e2e-parallel`, `prod-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **EV2 gating E2E**: `branch-ci-Azure-ARO-HCP-main-e2e-*` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- **Periodic cleanup**: `periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-*` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-cleanup.yaml`
- **Periodic E2E**: `periodic-ci-Azure-ARO-HCP-main-periodic-*-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic.yaml`
- **Image-updater tooling**: `periodic-ci-Azure-ARO-HCP-main-image-updater-*` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__image-updater.yaml`

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [CI Image Lifecycle](image-lifecycle.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI EV2 Integration](ev2-integration.md)
- [CI Cleanup](cleanup.md)
- [E2E Testing In CI](e2e-testing.md)
