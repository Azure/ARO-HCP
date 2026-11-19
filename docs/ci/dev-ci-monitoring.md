# DEV CI Monitoring and Alert Response

This is the runbook that Slack and PagerDuty link to for DEV CI alerts. It covers alert response, exporter health checks, routing maintenance and end-to-end validation. For a regional provision-health incident, use [DEV CI Regional Load Management](dev-region-load-management.md) instead.

The workload behind most of these alerts is still called `tenant-quota` for historical reasons. It began as a tenant-quota collector, but it is now the general DEV CI telemetry exporter: tenant capacity, subscription quota, E2E resource-group expiry and Prow job health all flow through the same path, and other CI sources can be added to it. This runbook deliberately does not catalogue collectors, metrics or alerts, since those change often. [`alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) is the source of truth for alert names, expressions, thresholds, annotations and routing, and [`tooling/tenant-quota/main.go`](../../tooling/tenant-quota/main.go) for the collectors the process registers.

## At A Glance

| Item | Value |
| --- | --- |
| Slack | [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ) |
| PagerDuty | team `ARO HCP Service Lifecycle`, service `ARO HCP Dev CI Alerts`, constant low urgency, Slack only |
| Azure Action Group | `opstool-pagerduty` |
| Azure Monitor Workspace | `opstool-monitor-usw3` |
| Exporter | deployment and service `tenant-quota-collector` in namespace `tenant-quota` |
| Dashboards | resource group `opstool-westus3`, dashboards `arohcpdevci-quota-{dev,int,stg,prod}` (defined by [`dashboards.bicep`](../../tooling/tenant-quota/dashboards.bicep)) |

## Architecture

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

The exporter runs on the standalone `opstool` AKS cluster; see [Opstool CI Platform](opstool.md) for the cluster, Prometheus and identity setup and [Dev-CI Topology](dev-ci-topology.md) for how it is rolled out. The exporter's own configuration, credentials and operating procedures are in [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md).

## Alert Response Workflow

When an incident fires:

1. Acknowledge the PagerDuty incident from [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ).
2. Create an on-call Jira bug if one does not exist yet, and link it from the incident.
3. Investigate, and record findings, evidence and links in the bug.
4. Assess priority and impact; if it needs continued attention, add the bug to the next shift's handover.

General remediation is intentionally not prescribed here. Each alert's `runbook_url` annotation says where to go next, which in practice means:

| Alerts | Guide |
| --- | --- |
| `AzureQuota*`, `TenantQuota*` | [Quota alerts](#quota-alerts) below |
| `E2EExpiredResourceGroups*`, `E2EExpiredResourceGroupStale` | [Expired resource groups](#expired-e2e-resource-groups) below and [CI Cleanup](cleanup.md) |
| `ProwCIHealthcheckProvisionSuccessRateLow` | [DEV CI Regional Load Management](dev-region-load-management.md) |
| Other `ProwCI*` job-health alerts | [CI Operations](operations.md) |
| `*MetricsStale`, `*CollectionStale`, `TenantQuotaCollectorUp`, `ProwCINoCachedRuns`, `ProwCIInvalidJobsDetected` | [Exporter Health Checks](#exporter-health-checks) |

### Quota alerts

The `TenantQuota*` alerts track how full a tenant's Entra directory object quota is. E2E runs consume it through the identities and app registrations they create, which is why CI pre-creates identities per slot instead of per run (see [Slot Assets](slot-assets.md#e2e-identities)) and why [CI Cleanup](cleanup.md) removes leftovers.

`AzureQuotaCritical` fires when `azure_quota_usage / azure_quota_limit` stays above its threshold for a given `quota_name`, `subscription_name` and `region`; the annotation names all three. The `source` label tells you which provider owns the quota: `compute`, `network` or `rbac`.

Role-assignment (`rbac`) quota cannot be raised with `az quota`; see [Role-assignment quota](e2e-capacity.md#role-assignment-quota) for how it is consumed and what to do about it. For `compute` and `network`, most DEV CI subscriptions allow self-service increases through the `Microsoft.Quota` API without a support ticket:

```bash
az extension add --name quota --upgrade

SUBSCRIPTION_ID=<subscription_id from the alert>
REGION=<region from the alert>
QUOTA_NAME=<quota_name from the alert, e.g. standardEDSv5Family>
PROVIDER=Microsoft.Compute   # Microsoft.Network for source=network

az quota show \
  --resource-name "$QUOTA_NAME" \
  --scope "/subscriptions/$SUBSCRIPTION_ID/providers/$PROVIDER/locations/$REGION"

# Reuse the resourceType from the output above (typically "dedicated" for
# compute, "shared" for network) so the update targets this specific quota.
RESOURCE_TYPE=<resourceType from the az quota show output>

az quota update \
  --resource-name "$QUOTA_NAME" \
  --scope "/subscriptions/$SUBSCRIPTION_ID/providers/$PROVIDER/locations/$REGION" \
  --limit-object value=<NEW_LIMIT> \
  --resource-type "$RESOURCE_TYPE"
```

Ask for real headroom (doubling the limit is a reasonable default) rather than the minimum that clears the alert, so the next load burst does not retrigger it. Re-run `az quota show` to confirm, and record the before and after values in the Jira bug. If the update fails or lands in a pending or manual-review state, the subscription needs an Azure support request from the portal's **Help + support** blade, with the subscription, region, quota name and target limit.

### Expired E2E resource groups

`E2EExpiredResourceGroupsInfo` and its escalation fire when a subscription accumulates E2E resource groups whose `deleteAfter` time has passed, which means the periodic cleanup for that subscription is not keeping up. Triage:

1. Take the subscription from the alert's `subscription_name` label and find its cleanup job using [Identifying the cleanup job for a subscription](cleanup.md#identifying-the-cleanup-job-for-a-subscription).
2. Check recent runs of that job in Prow. A passing job may simply be working through a backlog; a failing one needs its latest build log.
3. The job attempts every expired resource group on each run, so the log shows the outcome for all of them. Failures with one shared error point to a systemic problem, a scattered few to individual resources.

Failures you are likely to see:

- `InternalServerError` with `serviceProviderClusters` remaining is an RP-side problem, probably regional; check whether other shards in the region are affected and whether the region has an active incident.
- `ResourceGroupDeletionBlocked` means a child resource or managed resource group is preventing deletion.
- A timeout waiting for an already-deleting cluster means an earlier deletion request is stuck in ARM.
- Deny-assignment errors mean a managed resource group's deny assignments are blocking deletion (see AROSLSRE-10).

## Exporter Health Checks

Select the DEV subscription and get credentials for the `opstool` cluster:

```bash
az account set --subscription 1d3378d3-5a3f-4712-85a1-2485495dfc4b
az aks get-credentials \
  --resource-group opstool-westus3 \
  --name opstool-usw3 \
  --overwrite-existing
```

Check the workload, then its scrape endpoint through a port-forward:

```bash
kubectl -n tenant-quota get deployment,pods,service,endpoints
kubectl -n tenant-quota logs deployment/tenant-quota-collector --since=1h
kubectl -n tenant-quota port-forward service/tenant-quota-collector 8080:8080
```

With the port-forward running, from another terminal:

```bash
curl --fail --show-error http://127.0.0.1:8080/healthz
curl --fail --show-error http://127.0.0.1:8080/metrics
```

An empty or missing Kubernetes endpoint points at the deployment, pod readiness, the service selector or networking. A healthy endpoint with missing data points further along the path: source credentials or APIs, collector behaviour, Prometheus scraping, or Azure Monitor ingestion.

## Maintenance

Changes to DEV CI configuration, topology, infrastructure or tooling are deployed after merge by the [`branch-ci-Azure-ARO-HCP-main-dev-ci-pipeline-postsubmit`](https://prow.ci.openshift.org/?job=branch-ci-Azure-ARO-HCP-main-dev-ci-pipeline-postsubmit) Prow job; watch it to confirm the deployment completed. The files that define the monitoring setup are:

- [`tooling/tenant-quota/alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) for alert behaviour and routing.
- [`tooling/tenant-quota/dashboards.bicep`](../../tooling/tenant-quota/dashboards.bicep) for the per-environment dashboards, built from [`tenant-quota-dashboard.bicep`](../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep), with charted subscriptions and regions taken from `ci.<env>` and `opstool.tenantQuota` in [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml).
- [`opstool-alerting.bicep`](../../dev-infrastructure/templates/opstool-alerting.bicep) and [`pagerduty-actiongroup.bicep`](../../dev-infrastructure/modules/metrics/pagerduty-actiongroup.bicep) for the Action Group and its Key Vault wiring.

The PagerDuty and Slack side lives in their own administration UIs, so this is the configuration it is expected to have:

| Setting | Value |
| --- | --- |
| Team | `ARO HCP Service Lifecycle` |
| Service | `ARO HCP Dev CI Alerts` |
| Integration | `opstool Azure Monitor` |
| Escalation policy | `ARO HCP Dev CI - Slack Only` |
| Schedule | `ARO HCP Dev CI - 24x7 Ownership` |
| Urgency | Constant low urgency |
| Slack workspace / channel ID | `E030G10V24F` / `C0BMEC7UWQZ` (`#aro-hcp-alerts-rh-tenant`) |

The Red Hat Slack workspace must stay authorized under **Integrations > Extensions > Slack** in PagerDuty, and the service connection must target the workspace and channel above with notifications enabled for triggered, acknowledged, annotated, reassigned, reopened, unacknowledged and resolved events.

### Azure Integration Secret

The Action Group webhook reads its URL from the secret `pagerduty-azure-integration-url` in Key Vault `opstool-kv-usw3`. The value must be the full PagerDuty Microsoft Azure integration URL, not just the integration key. Set it without putting the URL in shell history:

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

The Action Group only picks up the new value when it is redeployed, so afterwards reconcile it through the dev-ci postsubmit job above.

## Validation

After routing or secret changes, validate the whole path:

1. Run a test notification for the `opstool-pagerduty` Action Group in the Azure portal.
2. Confirm an incident appears in the `ARO HCP Dev CI Alerts` PagerDuty service and in [`#aro-hcp-alerts-rh-tenant`](https://redhat.enterprise.slack.com/archives/C0BMEC7UWQZ), and that no personal notification is delivered.
3. Trigger one current rule from [`alerting.bicep`](../../tooling/tenant-quota/alerting.bicep) in a controlled way, then remove the condition.
4. Confirm the full lifecycle: trigger, Slack notification, recovery, PagerDuty resolution and the Slack resolution update.
