# CI

ARO HCP CI is split across this repository and the OpenShift CI configuration in `openshift/release`. This section is the concept-first map for how those pieces fit together: what each CI mode validates, how request paths cross tenants and subscriptions, how cleanup differs from execution, and where to go when you need to operate or change the system.

## Table Of Contents

### This Page

- [What This Section Covers](#what-this-section-covers)
- [CI Modes At A Glance](#ci-modes-at-a-glance)
- [Read This Next](#read-this-next)
- [Source Of Truth](#source-of-truth)
- [Related Documentation](#related-documentation)

### [CI Execution](execution.md)

- [What CI Validates](execution.md#what-ci-validates)
- [Execution Modes](execution.md#execution-modes)
- [PR Validation In DEV](execution.md#pr-validation-in-dev)
- [PR Validation In INT, STG, And PROD](execution.md#pr-validation-in-int-stg-and-prod)
- [EV2 Gating Jobs](execution.md#ev2-gating-jobs)
- [Periodic Jobs](execution.md#periodic-jobs)
- [CI Azure Flow](execution.md#ci-azure-flow)
- [DEV](execution.md#dev)
- [INT](execution.md#int)
- [STG](execution.md#stg)
- [PROD](execution.md#prod)
- [How An E2E Run Works](execution.md#how-an-e2e-run-works)
- [Authentication And Test Identity](execution.md#authentication-and-test-identity)
- [Environment Selection And Step Wiring](execution.md#environment-selection-and-step-wiring)
- [Request Path And Created Resources](execution.md#request-path-and-created-resources)
- [Cleanup Handoff](execution.md#cleanup-handoff)
- [Identity And Lease Mechanisms](execution.md#identity-and-lease-mechanisms)
- [Managed Identity Container Pool](execution.md#managed-identity-container-pool)
- [Prow, Ci-Operator, And Boskos](execution.md#prow-ci-operator-and-boskos)
- [MSI Mock Service Principal Pool](execution.md#msi-mock-service-principal-pool)
- [EV2 Commit Pinning](execution.md#ev2-commit-pinning)

### [CI Image Lifecycle](image-lifecycle.md)

- [Lifecycle Overview](image-lifecycle.md#lifecycle-overview)
- [CI Image Model](image-lifecycle.md#ci-image-model)
- [Shared CI Images](image-lifecycle.md#shared-ci-images)
- [Shared CI Build Root](image-lifecycle.md#shared-ci-build-root)
- [Shared CI Test Runner Image](image-lifecycle.md#shared-ci-test-runner-image)
- [What Ci-Operator Builds Inside A Job](image-lifecycle.md#what-ci-operator-builds-inside-a-job)
- [Runner Images Vs Service Images](image-lifecycle.md#runner-images-vs-service-images)
- [Why Local E2E Builds Many Images](image-lifecycle.md#why-local-e2e-builds-many-images)
- [Why Persistent-Environment E2E Builds Fewer Images](image-lifecycle.md#why-persistent-environment-e2e-builds-fewer-images)
- [Promotion Vs Push](image-lifecycle.md#promotion-vs-push)
- [How To Read Ci-Operator Logs](image-lifecycle.md#how-to-read-ci-operator-logs)
- [Where To Look](image-lifecycle.md#where-to-look)

### [CI Identity Leasing](identity-leasing.md)

- [Why Identity Leasing Exists](identity-leasing.md#why-identity-leasing-exists)
- [Managed Identity Container Pool](identity-leasing.md#managed-identity-container-pool)
- [Design And Runtime Behavior](identity-leasing.md#design-and-runtime-behavior)
- [Worker Coordination And State Files](identity-leasing.md#worker-coordination-and-state-files)
- [Prow, Ci-Operator, And Boskos Configuration](identity-leasing.md#prow-ci-operator-and-boskos-configuration)
- [Toggling Pooled Vs Non-Pooled Identities](identity-leasing.md#toggling-pooled-vs-non-pooled-identities)
- [Pool Sizing And Subscription Constraints](identity-leasing.md#pool-sizing-and-subscription-constraints)
- [Managing The Identity Pools](identity-leasing.md#managing-the-identity-pools)
- [Operational Notes And Troubleshooting](identity-leasing.md#operational-notes-and-troubleshooting)
- [MSI Mock Service Principal Pool](identity-leasing.md#msi-mock-service-principal-pool)
- [Pooled MSI Mock SPs With Boskos](identity-leasing.md#pooled-msi-mock-sps-with-boskos)
- [Infrastructure Setup](identity-leasing.md#infrastructure-setup)
- [Boskos Configuration](identity-leasing.md#boskos-configuration)
- [Lease Configuration](identity-leasing.md#lease-configuration)
- [Where To Look](identity-leasing.md#where-to-look)

### [CI EV2 Integration](ev2-integration.md)

- [Why EV2 Uses Prow Gating](ev2-integration.md#why-ev2-uses-prow-gating)
- [Current Environment Mapping](ev2-integration.md#current-environment-mapping)
- [How EV2 Maps To Prow Jobs](ev2-integration.md#how-ev2-maps-to-prow-jobs)
- [Programmatic Triggering And The `__e2e` Variant](ev2-integration.md#programmatic-triggering-and-the-__e2e-variant)
- [Commit Pinning And Test Image Fidelity](ev2-integration.md#commit-pinning-and-test-image-fidelity)
- [Gangway Authentication And prow-token](ev2-integration.md#gangway-authentication-and-prow-token)
- [Identifying Rollouts From Prow Metadata](ev2-integration.md#identifying-rollouts-from-prow-metadata)
- [Promotion Gating](ev2-integration.md#promotion-gating)
- [Where To Look](ev2-integration.md#where-to-look)

### [CI Cleanup](cleanup.md)

- [The Three Cleanup Modes](cleanup.md#the-three-cleanup-modes)
- [Strict per-test cleanup](cleanup.md#strict-per-test-cleanup)
- [Targeted environment teardown](cleanup.md#targeted-environment-teardown)
- [Background hygiene](cleanup.md#background-hygiene)
- [Why They Behave Differently](cleanup.md#why-they-behave-differently)
- [Why periodic cleanup is best-effort](cleanup.md#why-periodic-cleanup-is-best-effort)
- [Why E2E cleanup is strict](cleanup.md#why-e2e-cleanup-is-strict)
- [Why DEV cleanup actively deletes managed resource groups but public-cloud cleanup does not](cleanup.md#why-dev-cleanup-actively-deletes-managed-resource-groups-but-public-cloud-cleanup-does-not)
- [How Each Path Works](cleanup.md#how-each-path-works)
- [E2E test teardown](cleanup.md#e2e-test-teardown)
- [Templatize cleanup](cleanup.md#templatize-cleanup)
- [Periodic cleanup](cleanup.md#periodic-cleanup)
- [Where To Look](cleanup.md#where-to-look)

### [E2E Testing In CI](e2e-testing.md)

- [Why No Manual Testing Against INT, STG, Or PROD](e2e-testing.md#why-no-manual-testing-against-int-stg-or-prod)
- [Running Tests Via PR](e2e-testing.md#running-tests-via-pr)
- [Available Test Commands](e2e-testing.md#available-test-commands)
- [What These Jobs Test](e2e-testing.md#what-these-jobs-test)
- [Running Only Specific Tests](e2e-testing.md#running-only-specific-tests)
- [Example: Filter by Test Name](e2e-testing.md#example-filter-by-test-name)
- [Step-By-Step Process](e2e-testing.md#step-by-step-process)
- [Other Filter Examples](e2e-testing.md#other-filter-examples)
- [Test Suites And Labels](e2e-testing.md#test-suites-and-labels)
- [Periodic Tests](e2e-testing.md#periodic-tests)

### [CI Operations](operations.md)

- [Triggering Jobs](operations.md#triggering-jobs)
- [Pull-Request Commands](operations.md#pull-request-commands)
- [What Runs Automatically](operations.md#what-runs-automatically)
- [Inspecting Runs](operations.md#inspecting-runs)
- [Understanding EV2-Triggered Runs](operations.md#understanding-ev2-triggered-runs)
- [Modifying CI Configuration](operations.md#modifying-ci-configuration)
- [Maintaining Managed Identity Pools](operations.md#maintaining-managed-identity-pools)
- [Maintaining MSI Mock Service Principal Pools](operations.md#maintaining-msi-mock-service-principal-pools)
- [Troubleshooting](operations.md#troubleshooting)
- [Job Stuck Pending](operations.md#job-stuck-pending)
- [Test Failures In E2E Jobs](operations.md#test-failures-in-e2e-jobs)
- [Cleanup Failures](operations.md#cleanup-failures)
- [Getting Help](operations.md#getting-help)
- [Tiny Appendix: Key Job Families And Source Of Truth](operations.md#tiny-appendix-key-job-families-and-source-of-truth)

## What This Section Covers

- how PR validation, EV2 gating, and periodic hygiene differ
- how E2E jobs flow through test identities, Azure subscriptions, and RP scopes
- how CI images are built, reused inside OpenShift CI, and mirrored onward to ACRs
- where cleanup fits into the lifecycle of a CI run
- where the source of truth lives when you need to change CI behavior

## CI Modes At A Glance

- **PR validation in DEV** validates unmerged code against on-demand DEV service footprints. This is the only PR path that can exercise undeployed RP or infrastructure changes end to end.
- **PR validation in INT, STG, and PROD** runs test code against already-deployed environments. These jobs are useful for validating new or changed tests, not for validating unmerged service rollouts.
- **EV2 gating** triggers postsubmit E2E jobs against the exact commit being deployed, so promotion decisions can be made from a pinned rollout revision rather than from `HEAD`.
- **Periodic jobs** keep shared subscriptions healthy over time and run scheduled environment-health checks.

## Read This Next

- [CI Execution](execution.md) explains how CI works, what each execution mode validates, and how requests flow across tenants and subscriptions.
- [CI Image Lifecycle](image-lifecycle.md) explains the shared CI build root, job-local image graph, local E2E image injection, and the difference between CI promotion and ACR mirroring.
- [CI Identity Leasing](identity-leasing.md) explains the managed identity container pool, the MSI mock SP pool, and the current Boskos and ci-operator lease model.
- [CI EV2 Integration](ev2-integration.md) explains how EV2 selects Prow jobs, authenticates to Gangway, and pins runs to the exact rollout commit.
- [CI Cleanup](cleanup.md) explains why cleanup is intentionally split across strict per-test teardown, targeted environment teardown, and background hygiene.
- [E2E Testing In CI](e2e-testing.md) explains how to trigger E2E jobs from PRs and how to narrow test selection safely.
- [CI Operations](operations.md) explains how to trigger, inspect, troubleshoot, and change the CI system itself.

## Source Of Truth

- **This repository** holds product code, test code, EV2 wiring, and the local implementation of cleanup and identity-leasing behavior.
- **`openshift/release`** holds Prow job configuration, ci-operator configuration, and step-registry workflows for ARO HCP CI.
- **Generated Prow job manifests** under `ci-operator/jobs/Azure/ARO-HCP/` in `openshift/release` are outputs, not hand-edited source.

## Related Documentation

- [Environments](../environments.md)
- [Pipelines](../pipelines.md)
- [EV2 Deployment](../ev2-deployment.md)
- [Test Test Tenant Access](../sops/test-test-tenant-access.md)
