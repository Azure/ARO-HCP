# DEV CI Regional Load Management

This is the procedure for draining, rebalancing, or restoring new DEV CI runs when the health of an Azure region changes. For alert handling and Prow troubleshooting, see [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md) and [CI Operations](operations.md).

## How DEV Jobs Pick A Region

Most DEV jobs get their region from slot-manager. The pools in [`e2e-slots.yaml`](../../test/e2e-config/e2e-slots.yaml) use weighted region selection, where the job supplies relative weights in `LOCATION_WEIGHTS` and slot-manager derives the region deterministically from the run's `BUILD_ID`; the mechanism is described under [Catalog](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#catalog). A few consequences matter for operators:

- The weights give an approximate distribution over many runs, not a hard per-region concurrency limit.
- Nothing evaluates regional health or changes weights automatically. Changing them is a manual configuration change.
- A valid `MULTISTAGE_PARAM_OVERRIDE_LOCATION` bypasses the weights, so jobs that set one are pinned to a region.

The DEV jobs defined in `Azure-ARO-HCP-main.yaml` (`e2e-parallel`, `e2e-parallel-slow`, `e2e-parallel-ocp-nightly` and `e2e-parallel-inplace-upgrade`) are all weighted. The CAPZ `dev` job (`/test capz-e2e-dev`, in `Azure-ARO-HCP-main__capz-e2e.yaml`) and the HyperShift `e2e-aro-hcp` jobs (in the `openshift/hypershift` config) are pinned. The live job configuration in [`openshift/release`](https://github.com/openshift/release/tree/main/ci-operator/config/Azure/ARO-HCP) is the source of truth for the current weights and pinned regions.

## Assess Regional Health

Two signals are available, and neither is conclusive alone.

The periodic provision healthchecks, one job per region ([`westus3`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-westus3), [`centralus`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-centralus), [`canadacentral`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-canadacentral)), show whether ARO HCP infrastructure can be provisioned in each region. They do not run the E2E suite.

Recent `e2e-parallel` results grouped by the region each run selected give a broader signal, because tests can hit region-specific failures in Azure providers and features after provisioning has succeeded.

For both, look at the failing steps and artifacts first to rule out causes unrelated to the region, such as a code regression, credentials, slot acquisition, or the Prow build farm. Then discuss the combined evidence with the ARO HCP CI team, and change traffic only if you agree it will improve provision and E2E success. The `ProwCIHealthcheckProvisionSuccessRateLow` alert is the formal trigger for this decision, but it only reflects provisioning and has a conservative threshold, so acting earlier is reasonable when both signals justify it.

## Drain Or Rebalance Weighted Jobs

The change is to `LOCATION_WEIGHTS` in `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` in `openshift/release`. Every region in the pool must appear exactly once with a non-negative integer weight, and at least one weight must be non-zero. For example, to drain `westus3` and split new runs between the other two regions:

```yaml
LOCATION_WEIGHTS: westus3=0,canadacentral=1,centralus=1
```

To reduce traffic rather than stop it, give the affected region a lower positive weight. Apply the same change to every weighted DEV job that should avoid the region, since each job carries its own value. Do not add a location override to achieve this, because that would pin the job instead.

1. Open a PR against `openshift/release` with the weight change.
2. Run the repository's `make update` and confirm that only the intended source and generated job configuration changed.
3. Rehearse `e2e-parallel`, and confirm that slot-manager accepts the weights, picks an enabled region, and provisioning completes.
4. After the PR merges, check newly created runs: drained regions should not be selected, and enabled regions should provision and pass.

Weight changes only affect new ProwJobs. A retry is a new run with a new `BUILD_ID`, so it may land in a different enabled region.

## Restore Equal Distribution

Once provision and E2E health have recovered and the CI team agrees, repeat the procedure with equal positive weights:

```yaml
LOCATION_WEIGHTS: westus3=1,canadacentral=1,centralus=1
```

Check current healthcheck data and per-region `e2e-parallel` outcomes first rather than assuming that a drained region has recovered.

## Fail Over A Pinned Job

Weight changes do not touch pinned jobs, so move one only if that job itself needs to fail over. For the CAPZ `dev` job, set both `LOCATION` and `MULTISTAGE_PARAM_OVERRIDE_LOCATION` to the same region, because the CAPZ steps read `LOCATION` while slot-manager reads the override. For the HyperShift jobs, change `MULTISTAGE_PARAM_OVERRIDE_LOCATION` in the `openshift/hypershift` config. In either case, regenerate the configuration, rehearse only the affected job, and confirm its provision phase before merging.

Keep the regional healthcheck jobs probing all three regions throughout, so that recovery stays visible.
