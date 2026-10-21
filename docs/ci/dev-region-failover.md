# DEV CI Regional Load Management

Use this SOP to drain, rebalance, or restore new DEV CI runs when regional
health changes.

## Current Job Behavior

DEV jobs do not all use the same region-selection policy:

| Job | Region behavior |
| --- | --- |
| `e2e-parallel` | Selects one of `westus3`, `centralus`, or `canadacentral` for each run using `LOCATION_WEIGHTS`. |
| `e2e-parallel-inplace-upgrade` | Uses its explicit `MULTISTAGE_PARAM_OVERRIDE_LOCATION`. |
| CAPZ `dev` (`/test capz-e2e-dev`) | Uses its explicit `LOCATION` and matching `MULTISTAGE_PARAM_OVERRIDE_LOCATION`. |

The live `openshift/release` job configuration is the source of truth for the
current weights and pinned regions.

Slot manager selects the `e2e-parallel` region deterministically from
`BUILD_ID`. The configured weights provide an approximate distribution over
many runs, not a hard per-region concurrency limit. Slot manager does not
evaluate health or change weights automatically.

## Assess Regional Health

1. Review the recent regional provision-healthcheck jobs:
   - [`westus3`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-westus3)
   - [`centralus`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-centralus)
   - [`canadacentral`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-canadacentral)
2. Compare provision success by region. These healthchecks assess whether ARO
   HCP infrastructure can be provisioned in each region, but do not exercise
   the full E2E suite.
3. Review recent `e2e-parallel` outcomes grouped by the region selected for each
   run. E2E results provide a broader regional-health signal because tests can
   encounter region-specific failures in Azure providers and features after
   infrastructure provisioning has succeeded.
4. For both signals, inspect failing steps and artifacts to exclude failures
   unrelated to Azure region health, such as a code regression, credentials,
   leases, or the Prow build farm.
5. Discuss the combined evidence with the ARO HCP CI team. Change regional
   traffic only when the team agrees that it is likely to improve provision and
   E2E success.
   `ProwCIHealthcheckProvisionSuccessRateLow` is the alerting signal for this
   decision, but it covers only provision health. Operators may act before its
   conservative threshold is reached when provision-healthcheck and regional
   E2E evidence justify the change.

## Drain Or Rebalance `e2e-parallel`

Change only `LOCATION_WEIGHTS` for `e2e-parallel` in
`ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`.

Every configured region must appear exactly once with a non-negative integer
weight, and at least one weight must be non-zero. To drain `westus3` while
splitting new runs across the other regions:

```yaml
LOCATION_WEIGHTS: westus3=0,canadacentral=1,centralus=1
```

To reduce rather than stop traffic, give the affected region a lower positive
weight. Do not add an explicit location override: a valid
`MULTISTAGE_PARAM_OVERRIDE_LOCATION` bypasses weighted selection.

1. Open a PR against [`openshift/release`](https://github.com/openshift/release)
   with the weight change.
2. Run the repository's `make update` workflow and confirm that only the
   intended source and generated job configurations changed.
3. Rehearse `e2e-parallel`. Confirm that slot-manager accepts the weights,
   selects an enabled region, and completes infrastructure provisioning.
4. After the PR merges, inspect newly created runs to confirm that drained
   regions are not selected and that enabled regions provision and complete E2E
   tests successfully.

Weight changes affect only new ProwJobs. A retry is a new run with a new
`BUILD_ID` and may select a different enabled region.

## Restore Equal Distribution

After provision and E2E health recover and the ARO HCP CI team agrees to
restore traffic, repeat the procedure with equal positive weights:

```yaml
LOCATION_WEIGHTS: westus3=1,canadacentral=1,centralus=1
```

Use current provision-healthcheck data and regional `e2e-parallel` outcomes
rather than assuming that a previously drained region has recovered.

## Fail Over A Pinned Job

Weighted `e2e-parallel` changes do not affect pinned jobs. Change a pinned job
only when that job needs regional failover:

- For `e2e-parallel-inplace-upgrade`, update
  `MULTISTAGE_PARAM_OVERRIDE_LOCATION` in
  `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml`.
- For CAPZ `dev`, update both `LOCATION` and
  `MULTISTAGE_PARAM_OVERRIDE_LOCATION` in
  `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__capz-e2e.yaml` to the
  same region. CAPZ steps consume `LOCATION` directly while slot-manager
  consumes the override.

Regenerate the release configuration, rehearse only the affected job, and
confirm its provision phase before merging.

The regional healthcheck jobs must continue probing all three regions.

For alert handling, Prow troubleshooting, and CI configuration conventions, see
[DEV CI Monitoring and Alert Response](dev-ci-monitoring.md),
[CI Operations](operations.md), and the
[slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md).
