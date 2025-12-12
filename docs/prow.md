# Prow Jobs

ARO HCP uses OpenShift's Prow-based CI infrastructure for continuous integration and testing. Prow jobs are defined externally in the [OpenShift release repository](https://github.com/openshift/release/tree/master/ci-operator/jobs/Azure/ARO-HCP) and provide automated testing for pull requests and periodic maintenance tasks.

This document is intended for ARO HCP developers and SREs. It provides an overview of the Prow jobs used in the project, how to trigger them, and how to interpret their results.

## Table of Contents

- [Overview](#overview)
- [Job Categories](#job-categories)
  - [Presubmit Jobs](#presubmit-jobs)
    - [Images](#images)
    - [Frontend Simulation](#frontend-simulation)
    - [E2E Parallel](#e2e-parallel)
    - [Environment-Specific E2E Tests](#environment-specific-e2e-tests)
  - [Periodic Jobs](#periodic-jobs)
    - [Image Updater Tooling](#image-updater-tooling)
    - [Resource Group Cleanup](#resource-group-cleanup)
    - [Periodic E2E Tests](#periodic-e2e-tests)
- [EV2 Pipeline Integration](#ev2-pipeline-integration)
- [Working with Prow Jobs](#working-with-prow-jobs)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)
- [Related Documentation](#related-documentation)

## Overview

Prow is a Kubernetes-based CI/CD system originally developed for Kubernetes itself and now used across the OpenShift ecosystem. ARO HCP's Prow jobs are managed in the OpenShift release repository, separate from this codebase, which allows for centralized CI infrastructure management.

**Prow Dashboard:** [https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP](https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP)

The jobs are organized into two main categories:

- **Presubmit jobs**: Run on pull requests to validate changes before merging
- **Periodic jobs**: Run on a schedule to perform routine testing and maintenance

## Job Categories

### Presubmit Jobs

Presubmit jobs run automatically or on-demand for pull requests to the main branch. These jobs validate code changes before they are merged.

#### Quick Reference

| Job | Always Runs | Required | Environment |
|-----|-------------|----------|-------------|
| images | Yes | Yes | - |
| image-updater-images | Yes | Yes | - |
| periodic-images | Yes | Yes | - |
| frontend-simulation | Yes | Yes | - |
| e2e-parallel | Yes | No | Dev (westus3) |
| integration-e2e-parallel | No | No | Int (uksouth) |
| stage-e2e-parallel | No | No | Stage (uksouth) |
| prod-e2e-parallel | No | No | Prod (uksouth) |

#### Images

| Property | Value |
|----------|-------|
| **Job Names** | [`pull-ci-Azure-ARO-HCP-main-images`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-images)<br>[`pull-ci-Azure-ARO-HCP-main-image-updater-images`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-image-updater-images)<br>[`pull-ci-Azure-ARO-HCP-main-periodic-images`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-periodic-images) |
| **Status** | Always runs (required) |
| **Purpose** | Builds and validates container images for the project. The standard `images` job builds the main service images, while `image-updater-images` builds the image updater tooling variant, and `periodic-images` builds the images used by periodic jobs. |

---

#### Frontend Simulation

| Property | Value |
|----------|-------|
| **Job Name** | [`pull-ci-Azure-ARO-HCP-main-frontend-simulation`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-frontend-simulation) |
| **Status** | Always runs (required) |
| **Cluster** | build10 |
| **Step Registry** | [frontend-simulation](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=frontend-simulation) |
| **Purpose** | Simulates and tests the frontend service functionality. This job runs on a cluster with nested Podman capability to support containerized testing scenarios. |

---

#### E2E Parallel

| Property | Value |
|----------|-------|
| **Job Name** | [`pull-ci-Azure-ARO-HCP-main-e2e-parallel`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-e2e-parallel) |
| **Status** | Always runs, but optional (does not block merge) |
| **Environment** | Dev (westus3) |
| **Cluster** | build07 |
| **Step Registry** | [e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=e2e-parallel) |
| **Purpose** | Runs end-to-end tests in parallel mode against the dev environment. This job always runs on PRs but is marked optional, meaning failures won't block the PR from merging. |

---

#### Environment-Specific E2E Test Jobs

These optional jobs allow testing against specific Azure environments before merging changes in [ARO HCP E2E test case code](../test/e2e/). Using these jobs to validate changes in code of ARO HCP RP or ARO HCP infrastructure deployment is not possible, because changes in these components won't be available in Azure environments before a merge of given pull request.

##### Integration Environment E2E

| Property | Value |
|----------|-------|
| **Job Name** | [`pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel) |
| **Status** | Optional (runs only when triggered) |
| **Environment** | Int (uksouth) |
| **Step Registry** | [integration-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=integration-e2e-parallel) |
| **Purpose** | Runs end-to-end tests against the integration environment in the Microsoft Int tenant. |

##### Stage Environment E2E

| Property | Value |
|----------|-------|
| **Job Name** | [`pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel) |
| **Status** | Optional (runs only when triggered) |
| **Environment** | Stage (uksouth) |
| **Step Registry** | [stage-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=stage-e2e-parallel) |
| **Purpose** | Runs end-to-end tests against the staging environment in the Microsoft Stage tenant. |

##### Production Environment E2E

| Property | Value |
|----------|-------|
| **Job Name** | [`pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel`](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel) |
| **Status** | Optional (runs only when triggered) |
| **Environment** | Prod (uksouth) |
| **Step Registry** | [prod-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=prod-e2e-parallel) |
| **Purpose** | Runs end-to-end tests against the production environment in the Microsoft Prod tenant. |

> [!WARNING]
> Exercise caution when running tests against production environments. These should only be used when absolutely necessary.

### Periodic Jobs

Periodic jobs run on a regular schedule to maintain system health, perform routine tests, and clean up resources.

#### Image Updater Tooling

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-image-updater-image-updater-tooling`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-image-updater-image-updater-tooling) |
| **Schedule** | Daily at 2:00 AM UTC, Monday through Friday (`0 2 * * 1-5`) |
| **Step Registry** | [image-updater-tooling](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=image-updater&test=image-updater-tooling) |
| **Purpose** | Runs the image updater tooling to check for and update container image references. This helps keep the project's container images up to date with the latest patches and security fixes. |

---

#### Resource Group Cleanup

These jobs automatically delete expired resource groups across different environments to prevent resource accumulation from testing.

##### Integration Environment Cleanup

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-periodic-delete-expired-integration-resource-groups`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-delete-expired-integration-resource-groups) |
| **Schedule** | Every 30 minutes (`*/30 * * * *`) |
| **Environment** | Int (uksouth) |
| **Step Registry** | [delete-expired-integration-resource-groups](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=delete-expired-integration-resource-groups) |
| **Purpose** | Removes expired resource groups from the integration environment that were created during testing. |

##### Stage Environment Cleanup

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-periodic-delete-expired-stage-resource-groups`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-delete-expired-stage-resource-groups) |
| **Schedule** | Every 30 minutes (`*/30 * * * *`) |
| **Environment** | Stage (uksouth) |
| **Step Registry** | [delete-expired-stage-resource-groups](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=delete-expired-stage-resource-groups) |
| **Purpose** | Removes expired resource groups from the staging environment that were created during testing. |

##### Production Environment Cleanup

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-periodic-delete-expired-prod-resource-groups`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-delete-expired-prod-resource-groups) |
| **Schedule** | Every 30 minutes (`*/30 * * * *`) |
| **Environment** | Prod (uksouth) |
| **Step Registry** | [delete-expired-prod-resource-groups](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=delete-expired-prod-resource-groups) |
| **Purpose** | Removes expired resource groups from the production environment that were created during testing. |

---

#### Periodic E2E Tests

These jobs run comprehensive end-to-end tests on a schedule to catch regressions and ensure environment health.

##### Periodic Integration E2E

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel) |
| **Schedule** | January 1st at midnight (`0 0 1 1 *`) - placeholder only |
| **Environment** | Int (uksouth) |
| **Step Registry** | [integration-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=integration-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the integration environment. |

> [!NOTE]
> This job uses a placeholder schedule. It actually runs after each Int environment promotion via EV2 pipeline integration, so it runs frequently but not on a regular schedule.

##### Periodic Stage E2E

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel) |
| **Schedule** | Daily at 2:00 AM UTC (`0 2 * * *`) |
| **Environment** | Stage (uksouth) |
| **Step Registry** | [stage-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=stage-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the staging environment daily to validate the environment's health and catch any regressions. |

##### Periodic Production E2E

| Property | Value |
|----------|-------|
| **Job Name** | [`periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel) |
| **Schedule** | Daily at 2:00 AM UTC (`0 2 * * *`) |
| **Environment** | Prod (uksouth) |
| **Step Registry** | [prod-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=prod-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the production environment daily to ensure production environment health. |

## EV2 Pipeline Integration

The periodic E2E test jobs are integrated with EV2 (Express V2) deployment pipelines for Microsoft tenant environments (Int, Stage, and Prod). This integration enables automated testing as part of the deployment validation process.

### How Prow Jobs Link to EV2 Pipelines

The connection between Prow jobs and EV2 pipelines is established through configuration in the ARO-HCP repository:

1. **Configuration Files**: The [`config/config.msft.clouds-overlay.yaml`](../config/config.msft.clouds-overlay.yaml) file defines the `prowJobName` for each environment:

   ```yaml
   e2e:
     regionTest:
       prowJobName: periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel
       gatePromotion: true  # Optional: gates promotion on test success
   ```

2. **E2E Pipeline**: The [`test/e2e-pipeline.yaml`](../test/e2e-pipeline.yaml) file defines the E2E test execution pipeline that references the `prowJobName`:

   ```yaml
   variables:
   - name: PROW_JOB_NAME
     configRef: e2e.regionTest.prowJobName
   ```

3. **Environment-Specific Mapping**:
   - **Integration**: `periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel` - see `clouds.public.environments.int.defaults.e2e` in config
   - **Stage**: `periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel` - see `clouds.public.environments.stg.defaults.e2e` in config
   - **Production**: `periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel` - see `clouds.public.environments.prod.defaults.e2e` in config

### Identifying EV2 Rollouts from Prow Jobs

When a periodic E2E job runs as part of an EV2 pipeline, you can identify the associated EV2 rollout by examining the job's metadata:

1. **View Job Details**: Click on a specific job run in the [Prow dashboard](https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP)

2. **Check Annotations**: Look for the `ev2.rollout/` prefix in the job annotations, which provide:
   - **Rollout ID**: Unique identifier for the EV2 rollout (e.g., `ev2.rollout/ARO-HCP: "665a88398919"`)
   - **Build Number**: The ADO build number (e.g., `ev2.rollout/build: "144984048"`)
   - **Region**: The Azure region being tested (e.g., `ev2.rollout/region: "uksouth"`)
   - **SDP Pipeline**: The SDP pipeline identifier (e.g., `ev2.rollout/sdp-pipelines: "a848ce2e"`)

3. **Example Job URL**:

   ```text
   https://prow.ci.openshift.org/prowjob?prowjob=b2054fce-2218-4d65-8b20-2bc4a3a9df51
   ```

   This job will display the associated EV2 rollout information in its metadata.

### Promotion Gating

For environments where `gatePromotion: true` is set (like Integration), the success of the Prow E2E tests can gate the promotion to the next environment. This ensures that only validated deployments proceed through the release pipeline.

### For More Information

- See the [E2E Testing documentation](../test/e2e/README.md) for details on running and writing E2E tests
- See the [Pipelines documentation](pipelines.md) for information about the deployment pipeline system
- See the [EV2 Deployment documentation](ev2-deployment.md) for details on EV2 deployment processes

## Working with Prow Jobs

### Triggering Presubmit Jobs

To trigger optional presubmit jobs on a pull request, add a comment to the PR with the appropriate test command:

```text
/test e2e-parallel
```

To re-run a failed job:

```text
/retest
```

To re-run a specific job:

```text
/test <job-trigger>
```

### Viewing Job Results

Job results are reported directly on pull requests as GitHub checks. You can:

1. View the status of all jobs in the PR's "Checks" tab
2. Click on a specific job to see detailed logs and test output
3. Access the Prow dashboard for more detailed information and job history
4. Receive notifications in Slack - Prow jobs are configured to post notifications to the [#forum-ocp-testplatform](https://redhat.enterprise.slack.com/archives/CBN38N3MW) Slack channel

### Job Execution Environment

All Prow jobs run on OpenShift clusters managed by the OpenShift CI infrastructure:

- Jobs use Kubernetes for orchestration
- Jobs run with ci-operator for standardized build and test workflows
- Most jobs run on the build02 cluster, with some specialized jobs on build07 or build10

### Modifying Prow Jobs

Prow job definitions are maintained in the [openshift/release](https://github.com/openshift/release) repository, not in this repository. The actual job files are **generated** from configuration files, so you must edit the configs and regenerate.

To modify or add jobs:

1. Fork the [openshift/release](https://github.com/openshift/release) repository
2. Edit configuration files in [`ci-operator/config/Azure/ARO-HCP/`](https://github.com/openshift/release/tree/master/ci-operator/config/Azure/ARO-HCP):
   - `Azure-ARO-HCP-main.yaml` for presubmit and postsubmit jobs
   - `Azure-ARO-HCP-main__periodic.yaml` for periodic jobs
   - `Azure-ARO-HCP-main__image-updater.yaml` for image updater jobs
3. Generate the job definitions by running:

   ```bash
   make ci-operator-config
   make jobs
   ```

4. Submit a pull request to the openshift/release repository
5. The OpenShift CI team will review and merge the changes

> [!IMPORTANT]
>
> - The job files in `ci-operator/jobs/Azure/ARO-HCP/` are auto-generated - do not edit them directly
> - Changes to Prow jobs require approval from the OpenShift CI team and must follow their contribution guidelines
> - See the [README](https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/README.md) in the config directory for detailed instructions

## Best Practices

### For Developers

1. **Always wait for required jobs**: The `images` and `frontend-simulation` jobs must pass before merging
2. **Monitor job results**: Check job logs when tests fail to understand what went wrong
3. **Keep tests stable**: Flaky tests reduce confidence in the CI system
4. **Understand test limitations**: Environment-specific E2E tests (integration, stage, prod) only validate E2E test code changes, not RP or infrastructure changes

### For SREs

1. **Monitor periodic jobs**: Watch for recurring failures in periodic jobs that might indicate systemic issues
2. **Resource cleanup**: Verify that resource group cleanup jobs are running successfully to prevent cost accumulation
3. **Job maintenance**: Periodically review job configurations in the openshift/release repository to ensure they remain relevant
4. **Schedule awareness**: Be aware of when periodic jobs run to avoid conflicts with maintenance windows
5. **E2E failure notifications**: E2E test failures are automatically notified to Slack channels via the [Prow integration configuration](https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/.config.prowgen)

## Troubleshooting

### Common Issues

#### Job stuck in pending state

- Prow infrastructure may be experiencing high load
- Check the OpenShift CI status page for incidents
- Contact the OpenShift CI team if the issue persists

#### Test failures in E2E jobs

- Check the job logs for specific error messages
- Verify that the target environment is healthy
- Ensure your changes didn't introduce breaking changes

#### Resource group cleanup failures

- Verify that the cleanup jobs have appropriate permissions
- Check for resources with deletion locks
- Review Azure Activity Logs for detailed error information

### Getting Help

- For Prow infrastructure issues: Contact the OpenShift CI team in [#forum-ocp-testplatform](https://redhat.enterprise.slack.com/archives/CBN38N3MW)
- For ARO HCP-specific test failures: Review with the ARO HCP development team
- For job configuration changes: Submit a PR to the openshift/release repository

## Related Documentation

- [OpenShift CI Documentation](https://docs.ci.openshift.org/docs/)
- [Prow Documentation](https://docs.prow.k8s.io/)
- [ARO HCP E2E Testing](../test/e2e/README.md)
- [ARO HCP Environments](environments.md)
- [ARO HCP Pipelines](pipelines.md)
- [ARO HCP EV2 Deployment](ev2-deployment.md)
