# CI Operations

This document is the operator and maintainer view of ARO HCP CI. Use it when you need to inspect a failing run, change the underlying CI configuration, or troubleshoot.

For the execution model and cross-tenant request flow, start with [CI Execution](execution.md). For contributor-facing E2E usage including how to trigger jobs, see [E2E Testing In CI](e2e-testing.md).

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

## Modifying CI Configuration

ARO HCP Prow job definitions are maintained in `openshift/release`, not in this repository. The generated job manifests under `ci-operator/jobs/Azure/ARO-HCP/` are outputs and should not be edited directly.

When you change CI configuration in `openshift/release`, follow the release-repo regeneration workflow rather than hand-editing generated YAML. In practice that means using the repo's documented `make update` flow so ci-operator config, Prow jobs, and related generated artifacts stay in sync.

Also keep the ARO HCP-side wiring in mind:

- `config/config.msft.clouds-overlay.yaml` maps public-cloud environments to `prowJobName`
- `test/e2e-pipeline.yaml` passes `PROW_JOB_NAME` to EV2 gating

If one side changes without the other, the rollout path can drift even when the individual YAML files still look valid.

For the full list of ci-operator config files and step-registry components, see the "Where To Look" sections in [CI Image Lifecycle](image-lifecycle.md#where-to-look), [CI Identity Leasing](identity-leasing.md#where-to-look), and [CI EV2 Integration](ev2-integration.md#where-to-look).

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

## Key Job Families And Source Of Truth

- **PR build and simulation**: `pull-ci-Azure-ARO-HCP-main-images`, `pull-ci-Azure-ARO-HCP-main-frontend-simulation` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **DEV PR E2E**: `pull-ci-Azure-ARO-HCP-main-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` and `ci-operator/step-registry/aro-hcp/local-e2e/`
- **Higher-environment PR E2E**: `integration-e2e-parallel`, `stage-e2e-parallel`, `prod-e2e-parallel` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **Postsubmit image promotion and CD**: `branch-ci-Azure-ARO-HCP-main-images`, `branch-ci-Azure-ARO-HCP-main-images-push-postsubmit`, `branch-ci-Azure-ARO-HCP-main-cspr-pipeline-postsubmit` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`
- **Postsubmit CI base image**: `branch-ci-Azure-ARO-HCP-main-baseimage-generator-images` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__baseimage-generator.yaml`
- **Postsubmit global infra**: `branch-ci-Azure-ARO-HCP-main-global-pipeline-postsubmit` -> `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` (runs on changes to `config/config.yaml`, `observability/dashboards.yaml`, or `dev-infrastructure/`)
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
