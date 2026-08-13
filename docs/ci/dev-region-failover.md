# DEV CI Regional Failover And Failback

Use this SOP to move new DEV CI jobs between Azure regions when provision
health indicates that another region is likely to provide a significant
improvement.

The active region is `westus3`; the fallback regions are `centralus` and
`canadacentral`.

## Procedure

1. Review the recent regional provision-healthcheck jobs:
   - [`westus3`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-westus3)
   - [`centralus`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-centralus)
   - [`canadacentral`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-canadacentral)
2. Compare provision success by region. Check the failing steps and artifacts to
   exclude failures unrelated to Azure region health, such as a code
   regression, credentials, leases, or the Prow build farm.
3. Discuss the evidence with the ARO HCP CI team. Switch only when the team
   agrees that the target region is likely to provide a significant improvement
   in provision success.
   `ProwCIHealthcheckProvisionSuccessRateLow` is the alerting signal for this
   decision, but operators may act before its conservative threshold is reached.
4. Open a PR against [`openshift/release`](https://github.com/openshift/release)
   that changes all of the following to the selected region:

   | Configuration | Job | Variable |
   | --- | --- | --- |
   | `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` | `e2e-parallel` | `MULTISTAGE_PARAM_OVERRIDE_LOCATION` |
   | `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` | `e2e-parallel-inplace-upgrade` | `MULTISTAGE_PARAM_OVERRIDE_LOCATION` |
   | `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__capz-e2e.yaml` | `dev` (`/test capz-e2e-dev`) | `LOCATION` |

5. Run the `openshift/release` `make update` workflow and confirm that only the
   intended source and generated job configurations changed.
6. Before merging, rehearse all three affected jobs and confirm that they use
   the selected region and complete the provision phase successfully.
7. After the PR merges, confirm that newly created runs of the three jobs use
   the selected region and complete the provision phase successfully.

The regional healthcheck jobs must continue probing all three regions. Existing
ProwJobs are not moved by the configuration change.

## Failback

Repeat the same procedure to return to `westus3` or move to the other fallback.
Use current provision-healthcheck data and team agreement rather than assuming
that a previously active region has recovered.

For alert handling, Prow troubleshooting, and CI configuration conventions, see
[DEV CI Monitoring and Alert Response](dev-ci-monitoring.md) and
[CI Operations](operations.md).
