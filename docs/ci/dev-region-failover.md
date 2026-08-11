# DEV CI Regional Failover And Failback

Use this runbook to move the ARO HCP DEV presubmit workload away from an
unhealthy Azure region and later return it to the preferred region.

This procedure covers:

- `e2e-parallel`
- `e2e-parallel-inplace-upgrade`
- `/test capz-e2e-dev`

The preferred DEV region is `westus3`. Use `centralus` as the first fallback
and `canadacentral` as the second fallback.

Tracking:

- [ARO-27246: Write the DEV manual failover and failback runbook](https://redhat.atlassian.net/browse/ARO-27246)
- [ARO-27248: Define region drain and re-enable criteria from provision-phase health](https://redhat.atlassian.net/browse/ARO-27248)

## Scope And Safety

This is a CI traffic switch, not a product-region failover. It changes where
new on-demand DEV CI environments are provisioned. It does not move an existing
environment, migrate a running job, change INT/STG/PROD, or alter the regional
healthcheck jobs.

Do not use full E2E pass rate as the regional health signal. Test assertions,
product regressions, and unrelated build-farm failures can fail after regional
provisioning has succeeded. Base the drain and re-enable decision on the
provision-only healthchecks and corroborating failures in the provision phase.

## Automated Behavior And Manual Actions

| Behavior | Automated | Operator action required |
| --- | --- | --- |
| Run a provision-only probe in `westus3`, `centralus`, and `canadacentral` | Yes. Each region runs every two hours, staggered by 20 minutes. | Inspect the affected runs and artifacts. |
| Calculate the regional provision failure rate | Yes. The DEV CI exporter polls Prow every five minutes and retains a 24-hour run window. | Confirm that the data is current before acting. |
| Fire `ProwCIHealthcheckProvisionSuccessRateLow` | Yes. It fires when more than 40% of at least five completed regional probes failed, and the condition remains true for 30 minutes. | Acknowledge and investigate the incident. |
| Resolve the alert | Yes. Azure Monitor auto-resolves after the expression is clear for 10 minutes. | Confirm recovery is real rather than caused by missing telemetry. |
| Select a fallback region | No. | Apply the criteria in this runbook and record the decision. |
| Move new presubmit jobs to another region | No. | Change the live `openshift/release` configuration, regenerate it, obtain approval, and merge it. |
| Stop or move jobs that are already running | No. | Normally let them finish and clean up. Abort only when continued execution increases impact or cleanup cannot complete safely. |
| Return jobs to `westus3` | No. | Apply the re-enable criteria and repeat the configuration change. |

The alert expression, thresholds, and durations are defined in
[`tooling/tenant-quota/alerting.bicep`](../../tooling/tenant-quota/alerting.bicep).
The probe schedules and job definitions are maintained in `openshift/release`.

## Ownership And Escalation

The ARO HCP CI on-call or incident owner owns the drain and re-enable decision.
That person must record the evidence, selected region, affected jobs, and
rollback point in the incident Jira.

Use these escalation paths:

- DEV CI alert and incident coordination:
  [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ)
  and the `ARO HCP Dev CI Alerts` PagerDuty service
- ARO HCP provisioning or service-lifecycle failures: the ARO HCP Service
  Lifecycle owners
- Prow, ci-operator, or build-farm failures:
  [`#forum-ocp-testplatform`](https://redhat.enterprise.slack.com/archives/CBN38N3MW)
- CAPZ-specific failures: `#hcm-aro-team-capz`

An `openshift/release` approver must merge the actual region switch. Do not
bypass that repository's normal generation, review, or merge process during an
incident.

## Prerequisites

Before changing the active region:

1. Assign an incident owner and create or identify the incident Jira.
2. Confirm access to:
   - the [ARO HCP Prow dashboard](https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP)
   - the DEV CI PagerDuty service and alert Slack channel
   - a writable clone of [`openshift/release`](https://github.com/openshift/release)
3. Confirm that the exporter is collecting current Prow data. Follow
   [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md#exporter-health-checks)
   if the alert data may be stale.
4. Inspect the latest regional provision-only jobs:
   - [`westus3`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-westus3)
   - [`centralus`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-centralus)
   - [`canadacentral`](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-periodic-healthcheck-provision-canadacentral)
5. Confirm that the proposed fallback has no known Azure incident, quota
   exhaustion, or repeated provision-phase failure.

DEV slot pools use runtime-selected regions, so changing the job's runtime
region does not require changing
[`test/e2e-config/e2e-slots.yaml`](../../test/e2e-config/e2e-slots.yaml).

## Decision Policy

### Drain The Active Region

Drain the active region when
`ProwCIHealthcheckProvisionSuccessRateLow` is firing for it, unless the incident
owner has confirmed that the alert is caused by stale or incorrect telemetry.

The alert is intentionally conservative: it uses a 24-hour window, requires at
least five completed probes, requires more than 40% failures, and waits another
30 minutes before firing. An operator may switch earlier when all of the
following are true:

1. The latest two provision-only probes for the active region failed, or
   multiple DEV presubmits failed in the regional provision phase with the same
   region-specific failure signature.
2. The failure is not explained by repository code, shared global
   infrastructure, credentials, lease exhaustion, or a general Prow/build-farm
   incident.
3. The fallback region meets the eligibility criteria below.
4. The incident owner records why waiting for the alert threshold would cause
   avoidable CI impact.

One failed healthcheck without corroborating evidence is not enough to drain a
region.

### Select A Fallback

A fallback is eligible when:

1. Its latest two scheduled provision-only probes succeeded.
2. `ProwCIHealthcheckProvisionSuccessRateLow` is not active for that region.
3. There is no known Azure service incident or quota/capacity problem affecting
   the region.
4. The probe artifacts show that the full ARO HCP `Region` entrypoint
   provisioned successfully, not merely that the Prow pod started.

Choose `centralus` when both fallbacks are eligible. Choose `canadacentral` when
`centralus` is ineligible or is already the drained region. If neither fallback
is eligible, do not move the jobs blindly; escalate and keep the incident open.

### Re-enable A Drained Region

Re-enable a region only when:

1. The regional incident or Azure service issue is resolved or has a documented
   mitigation.
2. The latest three scheduled provision-only probes for the region succeeded.
3. `ProwCIHealthcheckProvisionSuccessRateLow` has resolved for the region.
4. At least one operator has reviewed the successful probe artifacts for the
   original failure signature.

Because the alert uses a 24-hour window, recovery samples can take time to clear
older failures. If the alert remains active only because of old samples, the
ARO HCP CI incident owner may approve re-enable after three consecutive
successful probes, but must record that exception and the supporting evidence
in the incident Jira.

Fail back to `westus3` when it is eligible. Do not remain on a fallback only to
avoid a second configuration change.

## Procedure

### 1. Record The Current State

In a current `openshift/release` checkout:

```bash
git fetch origin
git switch main
git pull --ff-only origin main

git grep -n -E \
  'as: (e2e-parallel|e2e-parallel-inplace-upgrade)|MULTISTAGE_PARAM_OVERRIDE_LOCATION' \
  -- ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml

git grep -n -E \
  'as: dev|LOCATION:' \
  -- ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__capz-e2e.yaml
```

At the time of writing, all three values are `westus3`. Treat the live
configuration as authoritative.

Record:

- the current region
- the target region
- the latest relevant healthcheck URLs
- the incident Jira
- the commit at `origin/main`

### 2. Change All Three Presubmits

Create a branch in `openshift/release` and update these source configuration
values together:

| Source file | Test entry | Variable |
| --- | --- | --- |
| `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` | `e2e-parallel` | `MULTISTAGE_PARAM_OVERRIDE_LOCATION` |
| `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` | `e2e-parallel-inplace-upgrade` | `MULTISTAGE_PARAM_OVERRIDE_LOCATION` |
| `ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__capz-e2e.yaml` | `dev` (`/test capz-e2e-dev`) | `LOCATION` |

Do not change:

- the three `periodic-healthcheck` regional jobs; they must continue probing all
  regions
- INT, STG, or PROD job locations
- the DEV slot catalog; its DEV pools are `runtime-selected`
- generated files directly

`MULTISTAGE_PARAM_OVERRIDE_LOCATION` is consumed by the ARO HCP lease and
write-config steps. The CAPZ workflow consumes `LOCATION` directly for its
on-demand ARO HCP environment and AKS/workload resources.

### 3. Regenerate And Review The Release Configuration

Run the repository's normal generation flow:

```bash
make update
git status --short
git diff --check
git diff
```

Confirm that the generated presubmit configuration contains the target region
for:

- `pull-ci-Azure-ARO-HCP-main-e2e-parallel`
- `pull-ci-Azure-ARO-HCP-main-e2e-parallel-inplace-upgrade`
- `pull-ci-Azure-ARO-HCP-main-capz-e2e-dev`

Reject the change if any of the three jobs is missing, if unrelated job
locations changed, or if the regional healthcheck jobs were modified.

### 4. Open And Merge The Release PR

Open an `openshift/release` PR that:

- links the incident Jira
- states the old and new regions
- includes the healthcheck evidence and decision criterion
- identifies the rollback region
- requests ARO HCP CI and relevant OpenShift CI review
- requests CAPZ review when the CAPZ presubmit is changed

Wait for the required generated-config checks and approvals. The switch takes
effect for newly created ProwJobs after the release configuration merges and is
deployed. Existing ProwJobs keep the environment captured when they were
created.

### 5. Validate The Switch

After merge:

1. Confirm the live generated Prow configuration contains the new region for
   all three jobs.
2. Trigger or observe one new `e2e-parallel` run and confirm:
   - lease acquisition exports the selected location
   - the rendered `config.yaml` artifact uses the target region
   - the regional provision step succeeds
3. Trigger `/test e2e-parallel-inplace-upgrade` on an appropriate ARO HCP PR and
   confirm its baseline and upgrade provisioning use the target region.
4. Trigger `/test capz-e2e-dev` on an appropriate ARO HCP PR and confirm the
   ARO HCP environment, AKS management cluster, and workload resources use the
   target region.
5. Confirm cleanup completed in the old and new regions.
6. Add the release PR and validation run links to the incident Jira.

The switch is validated when all three newly created jobs show the target
region in their configuration or logs and complete regional provisioning. A
later test assertion failure does not invalidate the regional switch if
provisioning and cleanup succeeded.

## Rollback And Second Fallback

Rollback uses the same release-config procedure:

1. Select the previous region only if it currently meets the fallback
   eligibility criteria.
2. Revert all three source values to that region.
3. Run `make update`, review generated changes, and merge the release PR.
4. Repeat the validation steps.

If the first fallback becomes unhealthy while the preferred region remains
drained, evaluate `canadacentral` using the same criteria and switch all three
jobs together. Do not split the jobs across regions unless an incident owner
documents a specific reason and a follow-up plan to restore one active region.

## Completion

Keep the incident open until the active region is stable and the validation
runs are linked. Record:

- drain and re-enable decision times
- alert and healthcheck evidence
- release PRs
- validation runs
- any exception to the normal criteria
- follow-up work for missing or misleading signals

Closing the implementation Jira is separate from resolving a regional
incident. Complete ARO-27246 and ARO-27248 only after the documentation PR that
introduced this runbook has merged.

## Sources Of Truth

| Concern | Source |
| --- | --- |
| Alert expression, threshold, duration, and auto-resolution | [`tooling/tenant-quota/alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) |
| Prow polling interval and 24-hour retention | [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) |
| DEV runtime-selected slot behavior | [`test/e2e-config/e2e-slots.yaml`](../../test/e2e-config/e2e-slots.yaml) |
| ARO HCP presubmit region variables | `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main.yaml` |
| CAPZ DEV presubmit region variable | `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__capz-e2e.yaml` |
| Regional provision-only job schedules | `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__periodic-healthcheck.yaml` |
| Region override propagation | `openshift/release: ci-operator/step-registry/aro-hcp/write-config/` and `aro-hcp/lease/acquire/` |
| Provision-only workflow | `openshift/release: ci-operator/step-registry/aro-hcp/provision-healthcheck/` |
