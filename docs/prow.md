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
  - [Postsubmit Jobs](#postsubmit-jobs)
    - [E2E test container image (`aro-hcp-e2e-tests`)](#e2e-test-container-image-aro-hcp-e2e-tests)
    - [Image Build, Push and CSPR CD](#image-build-push-and-cd)
    - [EV2 Gating E2E Tests](#ev2-gating-e2e-tests)
  - [Periodic Jobs](#periodic-jobs)
    - [Image Updater Tooling](#image-updater-tooling)
    - [Cleanup Jobs](#cleanup-jobs)
    - [Periodic E2E Tests](#periodic-e2e-tests)
- [Managed Identity Reuse for E2E Tests](#managed-identity-reuse-for-e2e-tests)
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
- **Postsubmit jobs**: Triggered by EV2 pipelines to run E2E gating tests against a specific commit
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
| **Job Names** | [pull-ci-Azure-ARO-HCP-main-images](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-images)<br>[pull-ci-Azure-ARO-HCP-main-image-updater-images](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-image-updater-images)<br>[pull-ci-Azure-ARO-HCP-main-periodic-images](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-periodic-images) |
| **Status** | Always runs (required) |
| **Purpose** | Builds and validates container images for the project. The standard `images` job builds the main service images, while `image-updater-images` builds the image updater tooling variant, and `periodic-images` builds the images used by periodic jobs. |

---

#### Frontend Simulation

| Property | Value |
|----------|-------|
| **Job Name** | [pull-ci-Azure-ARO-HCP-main-frontend-simulation](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-frontend-simulation) |
| **Status** | Always runs (required) |
| **Cluster** | build10 |
| **Step Registry** | [frontend-simulation](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=frontend-simulation) |
| **Purpose** | Simulates and tests the frontend service functionality. This job runs on a cluster with nested Podman capability to support containerized testing scenarios. |

---

#### E2E Parallel

| Property | Value |
|----------|-------|
| **Job Name** | [pull-ci-Azure-ARO-HCP-main-e2e-parallel](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-e2e-parallel) |
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
| **Job Name** | [pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel) |
| **Status** | Optional (runs only when triggered) |
| **Environment** | Int (uksouth) |
| **Step Registry** | [integration-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=integration-e2e-parallel) |
| **Purpose** | Runs end-to-end tests against the integration environment in the Microsoft Int tenant. |

##### Stage Environment E2E

| Property | Value |
|----------|-------|
| **Job Name** | [pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel) |
| **Status** | Optional (runs only when triggered) |
| **Environment** | Stage (uksouth) |
| **Step Registry** | [stage-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=stage-e2e-parallel) |
| **Purpose** | Runs end-to-end tests against the staging environment in the Microsoft Stage tenant. |

##### Production Environment E2E

| Property | Value |
|----------|-------|
| **Job Name** | [pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel](https://prow.ci.openshift.org/?job=pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel) |
| **Status** | Optional (runs only when triggered) |
| **Environment** | Prod (uksouth) |
| **Step Registry** | [prod-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&test=prod-e2e-parallel) |
| **Purpose** | Runs end-to-end tests against the production environment in the Microsoft Prod tenant. |

> [!WARNING]
> Exercise caution when running tests against production environments. These should only be used when absolutely necessary.

### Postsubmit Jobs

Postsubmit jobs run after a PR is merged to the main branch.

#### Image Build, Push and CSPR CD

| Job | Always Runs | Purpose |
|-----|-------------|---------|
| images | Yes | Build and promote all service images |
| baseimage-generator-images | Yes | Build and promote the CI base image |
| images-push | Yes | Push promoted images to the service ACR |
| cspr | Yes | Deploy to the [CSPR environment](cspr.md) |
| global-pipeline-postsubmit | No (config/infra changes only) | Deploy shared global resources to the dev environment |

#### EV2 Gating E2E Tests

EV2 gating E2E tests are triggered programmatically by EV2 pipelines via the Gangway API to run E2E gating tests against a specific ARO-HCP commit. Unlike periodic jobs, these jobs receive a `base_sha` parameter that pins the test execution to the exact commit being deployed, ensuring E2E tests validate the code that was actually promoted rather than HEAD.

These jobs are defined in the `e2e` variant configuration ([Azure-ARO-HCP-main__e2e.yaml](https://github.com/openshift/release/blob/master/ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml)) and use `run_if_changed: ^$` to prevent automatic triggering on merge — they are only triggered programmatically via the [prow-job-executor](https://github.com/Azure/ARO-Tools/tree/main/tools/prow-job-executor).

#### E2E test container image (`aro-hcp-e2e-tests`)

The **`aro-hcp-e2e-tests`** image is the OpenShift CI image that carries the `aro-hcp-tests` binary. The Dockerfile lives in this repository as [test/Containerfile.e2e](../test/Containerfile.e2e). CI wiring and promotion rules are maintained in [openshift/release](https://github.com/openshift/release).

**Promotion:** Successful merges to `Azure/ARO-HCP` `main` run the branch **image postsubmit** (see [`branch-ci-Azure-ARO-HCP-main-images`](https://prow.ci.openshift.org/?job=branch-ci-Azure-ARO-HCP-main-images)), which builds the image and pushes it to the CI app registry so other jobs and developers can pull a tag that matches hash of merge commit or `latest`.

**What is inside the image** (from [test/Containerfile.e2e](../test/Containerfile.e2e)):

- **Base:** `registry.ci.openshift.org/aro-hcp/aro-hcp-ci-images:aro-hcp-e2e-base-ci`
- **Source tree:** the full ARO-HCP checkout at `/opt/app-root/src/github.com/Azure/ARO-HCP`
- **Build:** it compiles **`tooling/hcpctl`**, **`tooling/templatize`**, **`test/prow-job-executor`** and **`test/aro-hcp-tests`**. Building **`aro-hcp-tests`** also runs **`az bicep build`** on the Bicep under `demo/bicep` and `test/e2e-setup/bicep`, writing JSON into `test/e2e/test-artifacts/generated-test-artifacts/` (those files are Makefile prerequisites for the test binary)
- **Image size / speed:** Go module and build caches are removed after the build (`go clean -cache -modcache`, and `go.work.sum` is dropped) so the runtime image stays smaller than a naive dev build
- **Permissions:** the working directory is world-writable (`chmod 777`) for CI workloads that expect an open tree

**Pull URL:** after promotion, the image is available from the build farm registry, for example:

```text
quay-proxy.ci.openshift.org/aro-hcp/aro-hcp-e2e-tests:latest
```

##### Pulling `aro-hcp-e2e-tests` with Podman

Read [Summary of available registries](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#summary-of-available-registries), the table contains link to **app.ci** cluster.

Follow [How do I gain access to QCI?](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#how-do-i-gain-access-to-qci) in the OpenShift CI docs for RBAC on **app.ci** and logging in to **`quay-proxy.ci.openshift.org`** (human users or service accounts). Once you can authenticate, use the pullspec above with `podman pull` (see the same page for the `podman login` pattern with your **app.ci** identity).

##### Integration Environment

| Property | Value |
|----------|-------|
| **Job Name** | [branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel](https://prow.ci.openshift.org/?job=branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel) |
| **Environment** | Int (uksouth) |
| **Step Registry** | [integration-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=e2e&test=integration-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the integration environment after EV2 promotions. Gates promotion to stage. |

##### Stage Environment

| Property | Value |
|----------|-------|
| **Job Name** | [branch-ci-Azure-ARO-HCP-main-e2e-stage-e2e-parallel](https://prow.ci.openshift.org/?job=branch-ci-Azure-ARO-HCP-main-e2e-stage-e2e-parallel) |
| **Environment** | Stage (uksouth) |
| **Step Registry** | [stage-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=e2e&test=stage-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the staging environment after EV2 promotions. |

##### Production Environment

| Property | Value |
|----------|-------|
| **Job Name** | [branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel](https://prow.ci.openshift.org/?job=branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel) |
| **Environment** | Prod (uksouth) |
| **Step Registry** | [prod-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=e2e&test=prod-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the production environment after EV2 promotions. |

> [!NOTE]
> These postsubmit jobs build the **`aro-hcp-e2e-tests`** image (see [E2E test container image](#e2e-test-container-image-aro-hcp-e2e-tests)) from source at the pinned commit, so the test binary always matches the code being deployed. The ARO-HCP commit is extracted from the `EV2_ROLLOUT_VERSION` and passed as `--base-sha` to the prow-job-executor.

### Periodic Jobs

Periodic jobs run on a regular schedule to maintain system health, perform routine tests, and clean up resources.

#### Image Updater Tooling

| Property | Value |
|----------|-------|
| **Job Name** | [periodic-ci-Azure-ARO-HCP-main-image-updater-image-updater-tooling](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-image-updater-image-updater-tooling) |
| **Schedule** | Daily at 2:00 AM UTC, Monday through Friday (`0 2 * * 1-5`) |
| **Step Registry** | [image-updater-tooling](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=image-updater&test=image-updater-tooling) |
| **Purpose** | Runs the image updater tooling to check for and update container image references. This helps keep the project's container images up to date with the latest patches and security fixes. |

---

#### Cleanup Jobs

All cleanup periodic jobs are defined in `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-cleanup.yaml`. For design rationale and cleanup architecture, see [Cleanup](cleanup.md).

##### Expired Resource Groups

These jobs delete expired test resource groups across environments. They run hourly at minute 35.

| Job | Environment | Prow Link |
|-----|-------------|-----------|
| `delete-expired-dev-prow-resource-groups` | Dev (prow) | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-dev-prow-resource-groups) |
| `delete-expired-dev-pers-resource-groups` | Dev (pers) | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-dev-pers-resource-groups) |
| `delete-expired-integration-resource-groups` | Int | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-integration-resource-groups) |
| `delete-expired-stage-resource-groups` | Stg | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-stage-resource-groups) |
| `delete-expired-prod-resource-groups` | Prod | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-prod-resource-groups) |

##### Expired App Registrations

| Job | Tenant | Schedule | Prow Link |
|-----|--------|----------|-----------|
| `delete-expired-red-hat-tenant-app-registrations` | Red Hat | Hourly at :35 | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-red-hat-tenant-app-registrations) |
| `delete-expired-msft-test-tenant-app-registrations` | MSFT test | Hourly at :35 | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-msft-test-tenant-app-registrations) |

##### Kusto Role Assignments

| Property | Value |
|----------|-------|
| **Job Name** | [periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-kusto-role-assignments](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-kusto-role-assignments) |
| **Schedule** | Daily at 4:00 AM UTC (`0 4 * * *`) |
| **Purpose** | Removes stale Kusto database principal assignments left behind by deleted test app registrations. |

##### Cleanup-Sweeper

| Property | `sweeper-rg-ordered` | `sweeper-shared-leftovers-dev` |
|----------|----------------------|-------------------------------|
| **Prow Link** | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-sweeper-rg-ordered) | [job](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-sweeper-shared-leftovers-dev) |
| **Schedule** | Every 4 hours at :05 (`5 */4 * * *`) | Hourly at :35 (`35 * * * *`) |
| **Purpose** | Policy-driven ordered resource-group cleanup across dev subscriptions. | Cleans orphaned shared resources such as ARM role assignments in dev subscriptions. |

---

#### Periodic E2E Tests

These jobs run comprehensive end-to-end tests on a schedule to catch regressions and ensure environment health. They always test against HEAD of the main branch.

> [!NOTE]
> For EV2 gating tests that run against a specific pinned commit, see [Postsubmit Jobs / EV2 Gating E2E Tests](#ev2-gating-e2e-tests).

##### Periodic Integration E2E

| Property | Value |
|----------|-------|
| **Job Name** | [periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel) |
| **Schedule** | January 1st at midnight (`0 0 1 1 *`) - placeholder only |
| **Environment** | Int (uksouth) |
| **Step Registry** | [integration-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=integration-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the integration environment. |

> [!NOTE]
> This job uses a placeholder schedule. It is triggered on-demand and always tests against HEAD.

##### Periodic Stage E2E

| Property | Value |
|----------|-------|
| **Job Name** | [periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel) |
| **Schedule** | Daily at 2:00 AM UTC (`0 2 * * *`) |
| **Environment** | Stage (uksouth) |
| **Step Registry** | [stage-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=stage-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the staging environment daily to validate the environment's health and catch any regressions. |

##### Periodic Production E2E

| Property | Value |
|----------|-------|
| **Job Name** | [periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel) |
| **Schedule** | Daily at 2:00 AM UTC (`0 2 * * *`) |
| **Environment** | Prod (uksouth) |
| **Step Registry** | [prod-e2e-parallel](https://steps.ci.openshift.org/job?org=Azure&repo=ARO-HCP&branch=main&variant=periodic&test=prod-e2e-parallel) |
| **Purpose** | Runs end-to-end parallel tests against the production environment daily to ensure production environment health. |

## Managed Identity Reuse for E2E Tests

The E2E suites use a **managed identity pool** backed by **Boskos leases** to avoid re‑creating Azure managed identities on every run while still allowing high parallelism and isolation.

### Design and runtime behavior

- **Two modes of operation**
  - **Pooled mode** (default in CI) is enabled when `POOLED_IDENTITIES=true`. In this mode tests reuse pre‑created "identity containers" (resource groups that hold the well‑known managed identities for a single HCP cluster).
  - **Non‑pooled mode** (`POOLED_IDENTITIES=false`) creates identities directly in the cluster resource group using suffixed names (e.g. `control-plane-<clusterName>`). This is mainly for local or ad‑hoc runs.
- **Per‑spec leasing protocol**
  - The implementation lives in [test/util/framework/identities_helper.go](../test/util/framework/identities_helper.go).
  - On startup, the test binary reads the `LEASED_MSI_CONTAINERS` environment variable, which contains a **space‑separated list of resource group names** provided by Boskos for the current job.
  - Those resource groups are written into a YAML state file as a list of entries, each with a **three‑state lease lifecycle**:
    - `free`: container is available to be used by any test.
    - `assigned`: container has been reserved for a specific Ginkgo spec but is not yet in use.
    - `busy`: container is actively being used by that spec.
  - Each spec is identified by a stable **spec ID** (`specID()`), derived from the Ginkgo spec text and the OS process ID (`"<FullText-with-dashes>|pid:<pid>"`).
  - At the start of a spec, `AssignIdentityContainers` calls `assignNTo(specID, N)` to atomically reserve the required number of containers by transitioning `free → assigned`. If there are not enough free entries, it returns `ErrNotEnoughFreeIdentityContainers` and the helper retries with backoff until containers become available or the context is cancelled.
  - When a spec actually needs a container (for Bicep/ARM deployments), `ResolveIdentitiesForTemplate` / `DeployManagedIdentities` call `useNextAssigned(specID)`, which transitions a single entry from `assigned → busy` and returns its resource group name.
  - During test cleanup, `releaseLeasedIdentities` transitions all containers leased by that spec back to `free` via `releaseByContainerName`, and performs best‑effort cleanup of:
    - Federated identity credentials in each managed identity.
    - Role assignments scoped to the identity container resource group.
- **File‑based IPC for Ginkgo workers**
  - The [openshift-tests-extension](https://github.com/openshift-eng/openshift-tests-extension) parallelization model runs a **parent test process** that spawns multiple **OS worker processes** for Ginkgo specs.
  - These worker processes coordinate identity leases via a **shared YAML state file** plus a **separate lock file**:
    - The lock file ensures that only one worker modifies the state file at a time.
    - Each leasing operation (`assignNTo`, `useNextAssigned`, `releaseByContainerName`, `getLeasedIdentityContainers`) follows the pattern: take the lock, load state from disk, modify it in memory, then persist the updated state back to disk.
  - The YAML state file is created on first use from `LEASED_MSI_CONTAINERS` and then treated as the **single source of truth** for the lifetime of the job.
- **Identity naming**
  - The set of managed identities in each container is fixed and defined in `NewDefaultIdentities()` in `identities_helper.go` (e.g. `cluster-api-azure`, `control-plane`, `cloud-controller-manager`, `image-registry`, etc.).
  - In pooled mode, these canonical names are used as‑is in every identity container resource group.
  - In non‑pooled mode, the same base names are suffixed with the cluster name to ensure uniqueness within the cluster resource group.

### Prow, ci-operator, and Boskos configuration

For background on how leases work in OpenShift CI, see:

- [Quota and Leases](https://docs.ci.openshift.org/docs/architecture/quota-and-leases/)
- [Step Registry – Leases](https://docs.ci.openshift.org/docs/architecture/step-registry/#leases)

- **Canonical slot catalog**
  - The E2E lease inventory now lives in [test/e2e-config/e2e-slots.yaml](../test/e2e-config/e2e-slots.yaml).
  - The catalog defines, per environment and pool:
    - deploy-environment aliases (`prow`, `ci01`, `int`, `stg`, `prod`),
    - Boskos slot resource type and slot count,
    - Azure region and subscription hash,
    - MSI container prefix and per-slot container count.
  - This catalog is the single maintained source of truth for:
    - `aro-hcp-tests slot-manager apply-identity-pool`,
    - `aro-hcp-tests slot-manager acquire|release`,
    - the ARO-HCP-managed Boskos block in `openshift/release`.
- **Boskos resource types**
  - Boskos now leases **slots**, not flat MSI container resource groups.
  - Each slot is a named Boskos resource, for example:
    - `aro-hcp-dev-westus3-slot-00`
    - `aro-hcp-dev-westus3-slot-01`
    - `aro-hcp-prod-uksouth-slot-09`
- The ARO-HCP slot tool rewrites only the managed slot sections of [core-services/prow/02_config/generate-boskos.py](https://github.com/openshift/release/blob/master/core-services/prow/02_config/generate-boskos.py); legacy Boskos types remain manually managed outside those markers. It validates the generated [_boskos.yaml](https://github.com/openshift/release/blob/master/core-services/prow/02_config/_boskos.yaml) against the slot catalog.
- **Runtime lease flow**
  - The release step registry keeps dedicated acquire/release steps, but they are now thin wrappers around `./test/aro-hcp-tests slot-manager acquire|release`.
  - For temporary release-side rehearsals against an unmerged ARO-HCP commit, the lease steps also support `ARO_HCP_SLOT_MANAGER_GIT_REF`. When set, they fetch that ref from `github.com/Azure/ARO-HCP` and run `slot-manager` via `go run` instead of the built binary.
  - Leave `ARO_HCP_SLOT_MANAGER_GIT_REF` unset for normal ARO-HCP PR jobs: the `aro-hcp-e2e-tests` image is already built from the PR source, so the override adds startup cost without improving fidelity.
  - `slot-manager acquire` obtains one Boskos slot for the job through the ci-operator lease proxy and writes shared artifacts in the top level of `SHARED_DIR`:
    - `aro-hcp-slot-state.yaml`: persisted slot metadata and the leased Boskos resource name.
    - `aro-hcp-slot-env.sh`: exported runtime values for the test-suite contract only: `CUSTOMER_SUBSCRIPTION`, `LOCATION`, `LEASED_MSI_CONTAINERS`, `ARO_HCP_E2E_SLOT_NAME`, and `ARO_HCP_E2E_SLOT_RESOURCE_TYPE`.
  - `LEASED_MSI_CONTAINERS` remains the compatibility contract used by the E2E framework, but it is now derived from the leased slot's MSI prefix and count rather than leased directly from Boskos.
  - `slot-manager acquire` resolves `CUSTOMER_SUBSCRIPTION` by matching `customer-*-subscription-name` Vault entries in `CLUSTER_PROFILE_DIR` against the selected slot pool before it writes `aro-hcp-slot-env.sh`.
  - The workflows now acquire the slot before `aro-hcp-write-config`, so the rendered `config.yaml` uses the leased slot's `LOCATION`.
  - `newLeasedIdentityPoolState` in `identities_helper.go` still treats `LEASED_MSI_CONTAINERS` as the input contract, so the pooled identity state machine inside the test framework remains unchanged.
- **Toggling pooled vs non‑pooled identities**
  - The test steps that actually run the `aro-hcp-tests` binary define `POOLED_IDENTITIES`:
    - `aro-hcp-test-local` ([ci-operator/step-registry/aro-hcp/test/local/aro-hcp-test-local-ref.yaml](https://github.com/openshift/release/blob/master/ci-operator/step-registry/aro-hcp/test/local/aro-hcp-test-local-ref.yaml)):
      - Sets `POOLED_IDENTITIES` with default `"true"`.
    - `aro-hcp-test-persistent` ([ci-operator/step-registry/aro-hcp/test/persistent/aro-hcp-test-persistent-ref.yaml](https://github.com/openshift/release/blob/master/ci-operator/step-registry/aro-hcp/test/persistent/aro-hcp-test-persistent-ref.yaml)):
      - Sets `POOLED_IDENTITIES` with default `"true"`.
  - In the test framework, `UsePooledIdentities()` reads this environment variable and routes identity provisioning:
    - `true`: use the Boskos‑backed identity containers and the lease state machine.
    - `false`: skip Boskos and create identities directly in the cluster resource group.

### Managing the identity pools

- **`slot-manager` CLI**
  - The `test/cmd/aro-hcp-tests/slot-manager` command creates and maintains the slot-backed identity container resource groups in each test subscription, manages runtime slot leases, and keeps the ARO-HCP-managed Boskos inventory in sync with the slot catalog.
  - It wraps a pre‑generated ARM template (`msi-pools.json`, built from `test/e2e-setup/bicep/msi-pools.bicep`) and applies it as Azure deployment stacks, one per slot:
    - The `apply-identity-pool` command is implemented under `slot-manager/identity-pool`.
    - It loads the canonical slot catalog, iterates every pool declared for the requested environment, and creates container RGs from each slot's prefix and count.
    - Each pool uses the catalog-declared `subscription_name` and `region`; the command does not take `CUSTOMER_SUBSCRIPTION` or `--region` selectors.
  - Example usage (run from the `test` image or a local build):
    - `./test/aro-hcp-tests slot-manager apply-identity-pool --environment dev`
    - `./test/aro-hcp-tests slot-manager apply-identity-pool --environment prod`
- **Keeping Boskos and the pool in sync**
  - Any time you change the slot catalog:
    - rewrite the ARO-HCP-managed Boskos block in `openshift/release` with `./test/aro-hcp-tests slot-manager sync-boskos-config --release-repo <path>`,
    - run `make update` in the `openshift/release` checkout,
    - validate the generated inventory with `./test/aro-hcp-tests slot-manager validate-boskos-config --release-repo <path>`,
    - re-apply the affected identity pools with `./test/aro-hcp-tests slot-manager apply-identity-pool ...`.

### Operational notes and troubleshooting

- **Analyzing test timing**
  - When the pool is saturated, tests **block inside `AssignIdentityContainers`** until containers are freed by other specs.
  - From Ginkgo's perspective, this wait time is part of the overall spec runtime, but the framework records dedicated **test steps** using `RecordTestStep`:
    - `"Assign N identity containers"`
    - `"Lease identity container"`
    - `"Release leased identities"`
  - When analyzing performance (either from Prow artifacts or local runs), you can subtract or separately report the time spent in these steps to distinguish **infra wait time** from **actual test logic time**.
- **Common failure modes**
  - **`expected envvar LEASED_MSI_CONTAINERS to not be empty`**:
    - The job did not request Boskos leases or the leases failed to be assigned.
    - Check the ci-operator job configuration and Boskos health in `openshift/release`.
  - **`no assigned identity containers available for <specID>`**:
    - The spec called `useNextAssigned` without first successfully calling `AssignIdentityContainers`, or it is attempting to lease more containers than it previously reserved.
    - Verify that the test reserves the correct number of containers at the beginning of the spec and that all `ResolveIdentitiesForTemplate` / `DeployManagedIdentities` calls stay within that reservation.
  - **Leaked role assignments / FICs in identity container resource groups**:
    - `releaseLeasedIdentities` attempts best‑effort cleanup by:
      - Listing and deleting all FICs for each managed identity in the container RG.
      - Listing role assignments for the RG and deleting only those whose scope starts with the RG's resource ID.
    - Persistent leaks typically indicate either Azure permission issues or unexpected resources created; in that case, investigate the identity container RG directly in Azure.

## MSI Mock Service Principal Pool

The dev provisioning step keeps using the static Boskos lease contract
`LEASED_MSI_MOCK_SP`. That lease is consumed only by the dev provision step in
`openshift/release`, which reads
[dev-infrastructure/openshift-ci/msi-mock-pool.yaml](../dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
directly to derive `miMockClientId`, `miMockPrincipalId`, and `miMockCertName`
for the generated override config.

This keeps mock-SP selection scoped to the provision step instead of carrying it
through the slot lease runtime contract, while preserving the existing dev
mock-SP pool inventory and the personal-dev default config.

### Infrastructure setup

The pool can also be provisioned separately from the rest of the mock identities
(and re-run whenever SPs need change). The number of identities is controlled
by the `MSI_MOCK_POOL_SIZE` variable in `dev-infrastructure/Makefile` (default 20).

```bash
cd dev-infrastructure/

# Create certificates in Key Vault, app registrations and role assignments
make create-msi-mock-pool

# Grant the pool SPs access to the E2E test subscription
make grant-msi-mock-pool-e2e-access
```

After creation, run [`dev-infrastructure/openshift-ci/populate-msi-mock-pool.sh`](../dev-infrastructure/openshift-ci/populate-msi-mock-pool.sh) to populate [`dev-infrastructure/openshift-ci/msi-mock-pool.yaml`](../dev-infrastructure/openshift-ci/msi-mock-pool.yaml) with the real client IDs and principal IDs:

```bash
make populate-msi-mock-pool
```

## EV2 Pipeline Integration

The E2E gating test jobs are integrated with EV2 (Express V2) deployment pipelines for Microsoft tenant environments (Int, Stage, and Prod). This integration enables automated testing as part of the deployment validation process, running tests against the specific ARO-HCP commit being deployed.

### How Prow Jobs Link to EV2 Pipelines

The connection between Prow jobs and EV2 pipelines is established through configuration in the ARO-HCP repository:

1. **Configuration Files**: The [config/config.msft.clouds-overlay.yaml](../config/config.msft.clouds-overlay.yaml) file defines the `prowJobName` for each environment:

   ```yaml
   e2e:
     regionTest:
       prowJobName: branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel
       gatePromotion: true  # Optional: gates promotion on test success
   ```

2. **E2E Pipeline**: The [test/e2e-pipeline.yaml](../test/e2e-pipeline.yaml) file defines the E2E test execution pipeline that references the `prowJobName`:

   ```yaml
   variables:
   - name: PROW_JOB_NAME
     configRef: e2e.regionTest.prowJobName
   ```

3. **Commit Pinning**: The `prow-job-executor` extracts the ARO-HCP commit SHA from the `EV2_ROLLOUT_VERSION` environment variable and passes it as `--base-sha` to trigger the job as a postsubmit. This ensures the E2E tests run against the exact commit being deployed, not HEAD.

4. **Environment-Specific Mapping**:
   - **Integration**: `branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel` - see `clouds.public.environments.int.defaults.e2e` in config
   - **Stage**: `branch-ci-Azure-ARO-HCP-main-e2e-stage-e2e-parallel` - see `clouds.public.environments.stg.defaults.e2e` in config
   - **Production**: `branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel` - see `clouds.public.environments.prod.defaults.e2e` in config

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
2. Edit configuration files in [ci-operator/config/Azure/ARO-HCP/](https://github.com/openshift/release/tree/master/ci-operator/config/Azure/ARO-HCP):
   - `Azure-ARO-HCP-main.yaml` for presubmit and postsubmit jobs
   - `Azure-ARO-HCP-main__e2e.yaml` for EV2 gating E2E postsubmit jobs
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
- [Use registries in the build farm](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/)
- [Prow Documentation](https://docs.prow.k8s.io/)
- [ARO HCP Cleanup](cleanup.md)
- [ARO HCP E2E Testing](../test/e2e/README.md)
- [ARO HCP Environments](environments.md)
- [ARO HCP Pipelines](pipelines.md)
- [ARO HCP EV2 Deployment](ev2-deployment.md)
