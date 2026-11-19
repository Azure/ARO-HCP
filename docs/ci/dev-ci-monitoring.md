# DEV CI Monitoring and Alert Response

This is the canonical runbook linked from Slack and PagerDuty for DEV CI alerts. Use it for alert response, exporter checks, routing maintenance, and end-to-end validation.

The deployed workload is still named `tenant-quota` for historical reasons. It began as a tenant-quota collector, but it is now the extensible DEV CI telemetry exporter rather than a quota-only tool.

For a regional provision-health incident, use
[DEV CI Regional Load Management](dev-region-failover.md).

## At A Glance

| Item | Value |
| --- | --- |
| Slack | [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ) |
| PagerDuty team | `ARO HCP Service Lifecycle` |
| PagerDuty service | `ARO HCP Dev CI Alerts` |
| Urgency and notification model | Constant low urgency / Slack only |
| Azure Action Group | `opstool-pagerduty` |
| Azure Monitor Workspace | `opstool-monitor-usw3` |
| Exporter | Deployment and service `tenant-quota-collector` in namespace `tenant-quota` |
| Azure dashboard | `opstool-westus3` resource group → dashboards `arohcpdevci-quota-{dev,int,stg,prod}` (Azure Portal → Dashboards → filter by name; see [dashboards.bicep](../../tooling/tenant-quota/dashboards.bicep) for the deployed resource names) |

## Scope and Name

`tenant-quota` is a historical deployment and directory name, not a boundary on the platform's scope. Current telemetry includes tenant capacity, subscription quota, and E2E resource-group expiry. The same telemetry path can accommodate upcoming Prow or other CI sources; this does not imply that those sources are deployed today.

Do not maintain a collector or metric catalog in this runbook. Use:

- [`tooling/tenant-quota/main.go`](../../tooling/tenant-quota/main.go) for the collectors registered by the running process
- [`tooling/tenant-quota/pkg/`](../../tooling/tenant-quota/pkg/) for metric behavior
- [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) for deployed DEV CI configuration

## Architecture

The alerting path is:

```text
Azure and CI APIs
  -> tenant-quota exporter
  -> opstool Prometheus
  -> opstool Azure Monitor Workspace
  -> Azure Monitor Prometheus alert rules
  -> opstool-pagerduty Action Group
  -> PagerDuty
  -> Slack
```

The exporter runs on the standalone `opstool` AKS platform. See [Opstool CI Platform](opstool.md) for the cluster, Prometheus, rollout, identity, and shared-resource architecture.

## Alert Response Workflow

Use the incident payload to identify the affected signal, but use [`tooling/tenant-quota/alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) as the source of truth for current alert names, expressions, thresholds, durations, annotations, and routing.

1. Acknowledge the PagerDuty incident from [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ).
2. Create an on-call Jira bug if one does not already exist for the issue, and link it from the PagerDuty incident.
3. Investigate the issue and record all findings, evidence, and relevant links in the Jira bug.
4. Assess the issue's priority and impact. If it warrants continued attention, include the Jira bug in the handover for the next shift.

This runbook intentionally does not prescribe general remediation. For
`ProwCIHealthcheckProvisionSuccessRateLow`, follow the
[DEV regional load-management SOP](dev-region-failover.md). Other operational
knowledge should be recorded in the Jira bug during the investigation and
promoted into a dedicated operational knowledge base once the response is
understood and repeatable.

### ImageUpdaterConsecutiveFailures: image-updater failing repeatedly

`ImageUpdaterConsecutiveFailures` fires when the image-updater Prow periodic
has failed or errored on `maxConsecutiveFailures` (default 3) consecutive
scheduled runs, from the image-updater monitor exporter. The job runs at
06:00/14:00/22:00 UTC on weekdays only, so the default threshold is roughly
one full weekday of failed runs. The alert description links the most recent
failing run directly and carries a classified failure reason (the failing
ci-operator step and its first error line, read from the run's junit step
graph; empty when the run produced no artifacts).

1. Check the failure reason in the alert description first; it names the
   failing step and error, e.g. a registry or GitHub API failure.
2. Open the run link in the alert description and skim the failure log.
3. If the failure reason is not obvious from the log, run the triage-job
   workflow on the run URL.
4. See [`tooling/image-updater/`](../../tooling/image-updater/) and
   [image-lifecycle.md](image-lifecycle.md) for what the job does; a
   persistent failure means image bumps into ARO-HCP are delayed.
5. If a recent bump itself broke CI, consider reverting the offending
   image-updater PR and letting the next scheduled run retry.

> **Overlap with `ProwCIScheduledJobPermafailing`:** the tenant-quota Prow
> collector ingests all ARO-HCP jobs (including image-updater), so that
> generic rule can also page on image-updater permafailing. This rule is
> intentionally kept separate: it fires faster (3 consecutive runs, roughly
> one weekday, vs the generic rule's 24h all-fail window) and its alert
> carries the offending run's URL and build ID. The same failure may
> therefore produce two alerts; treat the one with the run link as primary.
> The image-updater rules are also deliberately severity 3 (warning) rather
> than the severity 2 the tenant-quota staleness rules use, until they have
> been validated against live metrics; promote them when enabling
> `alertingEnabled`.

### ImageUpdaterRunStalled: image-updater run hung or missing

`ImageUpdaterRunStalled` fires when the image-updater Prow periodic has not
completed a run within the schedule-derived stall threshold: 9h
(`weekdayStallSeconds`) on Tuesday-Friday — the 8h gap between the
06:00/14:00/22:00 slots plus run time — and from 09:00 UTC on Monday, and
58h (`mondayStallSeconds`) before 09:00 UTC on Monday, spanning the ~56h
weekend gap since the Friday 22:00 run (the job does not run Saturday or
Sunday). The 09:00 Monday switchover keeps the weekend budget from masking
a hung Monday 14:00 run until Tuesday: by 09:00 a completed 06:00 run
leaves elapsed time well under 9h, and a hung one has already tripped the
weekend rule ~08:25. A hung run never completes, so no failure
result ever appears — the missing completion is the only signal. Common
causes are the updater waiting on GitHub or Tide state, or a wedged registry
client. Both thresholds were backtested against all completed runs
(Sep-Nov 2026): the weekday rule fired only on three genuinely missed
slots, and the Monday budget leaves ~2h grace for the 06:00 run before
firing ~08:00 UTC.

1. Open the job history link in the alert description and check whether a
   run is currently in progress and for how long.
2. If a run has been pending for hours, inspect the Prow job payload and
   consider interrupting the stuck run so the next scheduled run starts
   clean.
3. Note the weekday-only schedule when interpreting gaps: the rule does not
   fire on Saturday/Sunday, and a Friday-night stall is detected Monday
   morning (~08:00 UTC) rather than over the weekend.

### ImageUpdaterRunDurationExceeded: image-updater run too slow

`ImageUpdaterRunDurationExceeded` fires when the most recent completed run
took longer than `maxRunDurationSeconds` (default 1h). A slow success is
also actionable: it usually indicates registry or ACR throttling, or retry
backoff on transient API errors.

1. Open the offending run link in the alert description.
2. Look for repeated retry/backoff log lines to identify which registry or
   API is slow.

### ImageUpdaterScrapeFailed: monitor exporter unhealthy

`ImageUpdaterScrapeFailed` fires when the image-updater monitor exporter
has failed its Prow poll, has not scraped in over 15 minutes, or has
stopped emitting metrics entirely (pod down or scrape target lost — the
`absent_over_time` term in the rule expression covers the case where the
series go stale). While it fires, the other image-updater alerts cannot be
trusted. Check the exporter pod (namespace `image-updater`) as described in
[Exporter Health Checks](#exporter-health-checks), and connectivity to
`prow.ci.openshift.org`.

### ImageUpdaterMonitorBlind: monitor watching zero jobs

`ImageUpdaterMonitorBlind` fires when the monitor polls Prow successfully
but no monitored job has a completed run (`image_updater_jobs_matched == 0`).
This means the Prow job was renamed, aged out of Prow retention, the
monitor's `--job-regex` flag is wrong, or every recent run is hung and
never completed — and every other image-updater alert is blind while it
fires (empty metrics never trip the other rules). Check the
[job history](https://prow.ci.openshift.org/?job=periodic-ci-Azure-ARO-HCP-main-image-updater)
for a rename or a run pending for hours, then the monitor deployment's
`--job-regex` argument.

### AzureQuotaCritical: Bumping a Quota

`AzureQuotaCritical` fires when `azure_quota_usage / azure_quota_limit` exceeds
0.95 for a given `quota_name`/`subscription_name`/`region`, from the
`tenant-quota-collector` metrics described in [Architecture](#architecture).
The alert annotation identifies the exact quota, subscription, and region. The
alert's `source` label is one of `compute`, `network`, or `rbac` (see
[`tooling/tenant-quota/pkg/subscriptionquota`](../../tooling/tenant-quota/pkg/subscriptionquota));
use it to pick the right resource provider below. For an `rbac`-sourced alert
(role-assignment quota), follow [CI Identity Leasing](identity-leasing.md)
instead — role-assignment limits are not raised through `az quota`.

For `compute` and `network` sources, most DEV CI subscriptions allow
self-service quota increases through the `Microsoft.Quota` API, no Azure
support ticket required. Install the `quota` CLI extension if it isn't already
present, then find the current limit and raise it:

```bash
az extension add --name quota --upgrade

SUBSCRIPTION_ID=<subscription_id from the alert>
REGION=<region from the alert>
QUOTA_NAME=<quota_name from the alert, e.g. standardEDSv5Family>
# Use the alert's `source` label to pick the provider:
#   compute -> Microsoft.Compute
#   network -> Microsoft.Network
PROVIDER=Microsoft.Compute

az quota show \
  --resource-name "$QUOTA_NAME" \
  --scope "/subscriptions/$SUBSCRIPTION_ID/providers/$PROVIDER/locations/$REGION"
# Read `resourceType` from the `az quota show` output above (typically
# `dedicated` for compute quotas, `shared` for network quotas) and reuse it
# below so the update matches this specific quota.
RESOURCE_TYPE=<resourceType from the az quota show output>

az quota update \
  --resource-name "$QUOTA_NAME" \
  --scope "/subscriptions/$SUBSCRIPTION_ID/providers/$PROVIDER/locations/$REGION" \
  --limit-object value=<NEW_LIMIT> \
  --resource-type "$RESOURCE_TYPE"
```

Target a reasonable headroom increase (for example, double the current limit)
rather than the minimum needed to clear the alert, so a similar CI-load burst
does not immediately re-trigger it. Re-run `az quota show` to confirm the new
limit took effect, then record the before/after values and command used in the
Jira bug.

If `az quota update` fails or returns a pending/manual-review state, the
subscription requires an Azure support request instead; open one from the
Azure portal's **Help + support** blade with the subscription, region, quota
name, and target limit from the alert.

### E2EExpiredResourceGroupsInfo: Expired E2E Resource Groups

`E2EExpiredResourceGroupsInfo` fires when more than 10 E2E resource groups
in a single subscription have a `deleteAfter` timestamp in the past for at
least 2 hours.

**Triage steps:**

1. Identify the affected subscription from the alert's `subscription_name`
   label (e.g. "ARO HCP E2E Hosted Clusters - Prod - 01").
2. Map the subscription to its cleanup Prow job using
   [`e2e-slots.yaml`](../../test/e2e-config/e2e-slots.yaml) and the naming
   convention described in [CI Cleanup — Identifying the cleanup job for a
   subscription](cleanup.md#identifying-the-cleanup-job-for-a-subscription).
   Note that the legacy "ARO HCP E2E" subscription uses the indexless job
   name
   `periodic-ci-Azure-ARO-HCP-main-periodic-cleanup-delete-expired-prod-resource-groups`.
3. Check recent runs of that job in Prow. If the job is passing, the
   backlog may be clearing on its own. If the job is failing, examine
   the build log for the most recent failure.
4. Because the job attempts every discovered resource group in each run,
   the build log contains the deletion outcome for all expired RGs, not
   just one. Look for patterns: are all deletions failing with the same
   error (systemic issue), or is only a subset failing (isolated
   resource problem)?
5. Common failure patterns:
   - `InternalServerError` with `serviceProviderClusters` remaining:
     Azure RP-side issue, likely regional. Check if other shards in the
     same region are affected.
   - `ResourceGroupDeletionBlocked`: a child resource or managed RG is
     preventing deletion.
   - Timeout waiting for already-deleting cluster: a prior deletion
     request is stuck in Azure ARM.
   - Deny assignment errors: managed RG deny assignments blocking
     deletion (see AROSLSRE-10).
6. For regional RP issues, check for active incidents in the affected
   region and coordinate with the RP team.

## Exporter Health Checks

Select the DEV subscription and obtain credentials for the `opstool` cluster:

```bash
az account set --subscription 1d3378d3-5a3f-4712-85a1-2485495dfc4b
az aks get-credentials \
  --resource-group opstool-westus3 \
  --name opstool-usw3 \
  --overwrite-existing
```

Check the workload and its scrape endpoint:

```bash
kubectl -n tenant-quota get deployment,pods,service,endpoints
kubectl -n tenant-quota logs deployment/tenant-quota-collector --since=1h
kubectl -n tenant-quota port-forward service/tenant-quota-collector 8080:8080
```

The image-updater monitor exporter is checked the same way in the
`image-updater` namespace (`deployment/image-updater-monitor`).

While the port-forward is running, use another terminal:

```bash
curl --fail --show-error http://127.0.0.1:8080/healthz
curl --fail --show-error http://127.0.0.1:8080/metrics
```

An empty or missing Kubernetes endpoint points to deployment, pod-readiness, service-selector, or networking problems. A healthy endpoint with absent data points toward source credentials, source APIs, collector behavior, Prometheus scraping, or Azure Monitor ingestion.

## Troubleshooting by Category

| Category | Durable guide |
| --- | --- |
| Tenant capacity, subscription quota, and identity-pool constraints | [CI Identity Leasing](identity-leasing.md) |
| Expired E2E resource groups and cleanup health | [CI Cleanup](cleanup.md) |
| Prow, job, and other CI execution signals | [CI Operations](operations.md) |
| Exporter runtime, deployment, credentials, and tenant management | [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md) |
| `opstool` AKS, Prometheus, identities, secrets, and rollout | [Opstool CI Platform](opstool.md) |

## Maintenance

Use authoritative files instead of copying evolving inventories:

- [`tooling/tenant-quota/alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) defines alert behavior and routing.
- [`tooling/tenant-quota/dashboards.bicep`](../../tooling/tenant-quota/dashboards.bicep) defines the per-environment (Dev/Int/Stg/Prod) Azure dashboards, built from [`tenant-quota-dashboard.bicep`](../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep).
- [`tooling/tenant-quota/main.go`](../../tooling/tenant-quota/main.go) registers collectors.
- [`tooling/tenant-quota/pkg/`](../../tooling/tenant-quota/pkg/) implements metric behavior.
- [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) supplies deployment configuration.
- [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md) documents exporter operation and credentials.
- [Opstool CI Platform](opstool.md) documents the hosting platform and rollout.

Changes to DEV CI configuration, topology, infrastructure, or tooling are deployed automatically after merge by the [`branch-ci-Azure-ARO-HCP-main-dev-ci-pipeline-postsubmit`](https://prow.ci.openshift.org/?job=branch-ci-Azure-ARO-HCP-main-dev-ci-pipeline-postsubmit) Prow job. Monitor that job to confirm the deployment completed.

The intended PagerDuty configuration is:

| Setting | Intended value |
| --- | --- |
| Team | `ARO HCP Service Lifecycle` |
| Service | `ARO HCP Dev CI Alerts` |
| Integration | `opstool Azure Monitor` |
| Escalation policy | `ARO HCP Dev CI - Slack Only` |
| Schedule | `ARO HCP Dev CI - 24x7 Ownership`, currently containing the single responder |
| Urgency | Constant low urgency |
| Slack channel | `#aro-hcp-alerts-rh-tenant` |
| Slack workspace ID | `E030G10V24F` |
| Slack channel ID | `C0BMEC7UWQZ` |

The Red Hat Slack workspace must remain authorized under **Integrations > Extensions > Slack**. The PagerDuty service connection must target workspace `E030G10V24F` and channel `C0BMEC7UWQZ`, with notifications enabled for triggered, acknowledged, annotated, reassigned, reopened, unacknowledged, and resolved events.

### Azure Integration Secret

The Action Group webhook is stored in Key Vault `opstool-kv-usw3` as secret `pagerduty-azure-integration-url`. Its value must be the full PagerDuty Microsoft Azure integration URL, not only an integration key.

Set it without putting the URL in shell history:

```bash
read -rsp "PagerDuty Microsoft Azure integration URL: " PAGERDUTY_AZURE_INTEGRATION_URL
printf '\n'
az keyvault secret set \
  --vault-name opstool-kv-usw3 \
  --name pagerduty-azure-integration-url \
  --value "$PAGERDUTY_AZURE_INTEGRATION_URL" \
  --output none
unset PAGERDUTY_AZURE_INTEGRATION_URL
```

After changing the secret, reconcile the Action Group through the `branch-ci-Azure-ARO-HCP-main-dev-ci-pipeline-postsubmit` job so its webhook receiver reads the new value.

## Validation

Validate the complete path after routing or secret changes:

1. In the Azure portal, run a test notification for the `opstool-pagerduty` Action Group.
2. Confirm that an incident appears in the `ARO HCP Dev CI Alerts` PagerDuty service and in [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ).
3. Confirm that no personal notification is delivered.
4. Use a controlled trigger for one current rule from [`alerting.bicep`](../../tooling/tenant-quota/alerting.bicep), then remove the condition.
5. Confirm the full incident lifecycle: trigger, Slack notification, recovery, PagerDuty resolution, and Slack resolution update.

## Sources of Truth

| Concern | Source |
| --- | --- |
| Alert names, expressions, thresholds, durations, annotations, and routing | [`tooling/tenant-quota/alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) |
| Per-environment quota dashboards (panels, subscriptions, regions charted) | [`tooling/tenant-quota/dashboards.bicep`](../../tooling/tenant-quota/dashboards.bicep), [`tenant-quota-dashboard.bicep`](../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep), and `ci.<env>.regions` / `opstool.tenantQuota` in [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) |
| Registered collectors and HTTP endpoints | [`tooling/tenant-quota/main.go`](../../tooling/tenant-quota/main.go) |
| Metric collection and credential behavior | [`tooling/tenant-quota/pkg/`](../../tooling/tenant-quota/pkg/) |
| DEV CI deployment configuration | [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) |
| Exporter deployment and operator procedures | [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md) |
| Opstool topology and workload ownership | [`topology-dev-ci.yaml`](../../topology-dev-ci.yaml) and [Opstool CI Platform](opstool.md) |
| Action Group resource and Key Vault wiring | [`opstool-alerting.bicep`](../../dev-infrastructure/templates/opstool-alerting.bicep) and [`pagerduty-actiongroup.bicep`](../../dev-infrastructure/modules/metrics/pagerduty-actiongroup.bicep) |
| PagerDuty objects and Slack connection | PagerDuty and Slack administration UIs |

Do not duplicate evolving alert, collector, or metric catalogs in this document.
