# CI

ARO HCP CI is split between this repository and the OpenShift CI configuration in `openshift/release`. This repository holds the product and test code, the EV2 wiring and most of the behavior that CI jobs execute. `openshift/release` holds the Prow job definitions, the ci-operator configuration and the step-registry workflows that run it. These pages explain how the pieces fit together, how to work with them, and where to look when something breaks.

## CI Modes At A Glance

- **PR validation in DEV** tests unmerged code against a service footprint that the job provisions on demand. It is the only PR path that can exercise undeployed RP or infrastructure changes end to end.
- **PR validation in INT, STG and PROD** runs test code against environments that are already deployed. These jobs are useful for validating new or changed tests, not unmerged service changes.
- **EV2 gating** runs postsubmit E2E jobs against the exact commit being rolled out, so promotion decisions rest on a pinned revision rather than on `HEAD`.
- **Periodic jobs** keep the shared subscriptions healthy and run scheduled environment checks.

## How CI Works

- [CI Execution](execution.md) is the place to start. It explains what each mode validates, how requests cross tenants and subscriptions in each environment, and how slots, run-cost tagging and configuration overrides fit into a run.
- [CI Image Lifecycle](image-lifecycle.md) covers the shared build root, the images built inside a job, and the difference between CI promotion and mirroring to ACR.
- [CI EV2 Integration](ev2-integration.md) explains how EV2 selects Prow jobs, authenticates to Gangway and pins runs to a rollout commit. [EV2 Retry Catcher](ev2-retry-catcher.md) covers the narrow case where a known-issue failure triggers one automatic retry of a gating run.
- [CI Cleanup](cleanup.md) explains why cleanup is split between strict per-test teardown, targeted environment teardown and background hygiene.

## E2E Slots, Identities And Capacity

E2E environments are acquired as slots from `slot-manager`, whose [design document](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md) specifies the catalog, the acquire and release lifecycle and the runtime contract. The pages below build on it.

- [E2E Slot Assets](slot-assets.md) describes the three assets that come with a slot (E2E identities, mock identities and infrastructure identities), why each exists, and how to provision and validate them.
- [DEV Mock Identities](dev-mock-identities.md) explains what each mock identity stands in for in DEV, why it holds the roles it does, and how slot mock identities and their certificates are prepared and rotated.
- [E2E Capacity And Quotas](e2e-capacity.md) explains the Azure limits that bound the slot catalog, how to check a catalog against them, and how to change capacity safely.
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md) is the procedure for adding an ARO-HCP-managed customer subscription in DEV, INT, STG or PROD. [External Subscription Management](external-subscription-management.md) covers onboarding and recurring maintenance for subscriptions owned by another team.
- [Cluster Profile Secret Contract](cluster-profile-secret-contract.md) defines the supported cluster profile fields, which scope a consumer may read, and why the transitional `subscription-*` keys are being retired.
- [DEV CI Regional Load Management](dev-region-load-management.md) describes how to drain, rebalance and restore DEV presubmit traffic across regions.

## Running And Troubleshooting CI

- [E2E Testing In CI](e2e-testing.md) explains how to trigger E2E jobs from a PR and how to narrow the tests that run.
- [In-Place Upgrade Presubmit](inplace-upgrade-presubmit.md) and [HyperShift v2 Checks](hypershift-v2-presubmit.md) describe two specialised presubmits, and [CAPZ E2E Jobs](capz-e2e.md) describes the jobs that run Cluster API Provider Azure against ARO HCP environments.
- [Supply-Chain Presubmit](supply-chain-presubmit.md) explains the `make verify-supply-chain` check that blocks committed agent and editor configuration, including its threat model and what it deliberately leaves to human review.
- [CI Operations](operations.md) covers inspecting runs, post-job observability artifacts, changing CI configuration and troubleshooting, and keeps a small appendix of job families.
- [Service Cluster Consolidation](system-pool-only.md) documents the single-node-pool mode that CI and personal DEV service clusters use.
- [CI Daily Digest Instructions](periodic-analysis-instructions.md) are the instructions the scheduled digest agent reads; they are not a human guide.

## DEV CI Infrastructure

- [Dev-CI Topology](dev-ci-topology.md) explains what the standalone `dev-ci` rollout owns and why part of it is applied on demand by an Owner.
- [Opstool CI Platform](opstool.md) describes the AKS platform that hosts the DEV CI tools.
- [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md) is the runbook for DEV CI alerts, exporter checks and routing.
- [AMW Platform Observability](amw-platform-observability.md) explains the Azure Monitor Workspace pane in the post-job observability report.

## Source Of Truth

- This repository holds product code, test code, EV2 wiring and the slot catalog (`test/e2e-config/e2e-slots.yaml`).
- `openshift/release` holds the Prow job configuration, the ci-operator configuration and the step-registry workflows. The job manifests generated under `ci-operator/jobs/Azure/ARO-HCP/` are outputs and are never edited by hand.
- The CodeQL analysis is a GitHub Actions workflow in this repository, `.github/workflows/codeql-analysis.yml`.
- [Maintenance Guide](MAINTENANCE.md) describes how these docs are organized and the rules for changing them.

## Related Documentation

- [Environments](../environments.md)
- [Grafana Dashboards](../grafana-dashboards.md)
- [Pipelines](../pipelines.md)
- [EV2 Deployment](../ev2-deployment.md)
- [Manually Trigger an E2E Gate Run](../sops/manual-e2e-gate-run.md)
- [Test Test Tenant Access](../sops/test-test-tenant-access.md)
