# DEV CI Monitoring and Alert Response

This is the canonical runbook linked from Slack and PagerDuty for DEV CI alerts. Use it for alert response, exporter checks, routing maintenance, and end-to-end validation.

The deployed workload is still named `tenant-quota` for historical reasons. It began as a tenant-quota collector, but it is now the extensible DEV CI telemetry exporter rather than a quota-only tool.

For a regional provision-health incident, use
[DEV CI Regional Failover And Failback](dev-region-failover.md).

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
| Azure dashboard | [DEV CI quota dashboard](https://portal.azure.com/#@redhat0.onmicrosoft.com/dashboard/arm/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourcegroups/dashboards/providers/microsoft.portal/dashboards/901b128a-124f-43e6-a797-5fcf3d1e83fe) |

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
[DEV regional failover SOP](dev-region-failover.md). Other operational
knowledge should be recorded in the Jira bug during the investigation and
promoted into a dedicated operational knowledge base once the response is
understood and repeatable.

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
| Registered collectors and HTTP endpoints | [`tooling/tenant-quota/main.go`](../../tooling/tenant-quota/main.go) |
| Metric collection and credential behavior | [`tooling/tenant-quota/pkg/`](../../tooling/tenant-quota/pkg/) |
| DEV CI deployment configuration | [`config/config-dev-ci.yaml`](../../config/config-dev-ci.yaml) |
| Exporter deployment and operator procedures | [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md) |
| Opstool topology and workload ownership | [`topology-dev-ci.yaml`](../../topology-dev-ci.yaml) and [Opstool CI Platform](opstool.md) |
| Action Group resource and Key Vault wiring | [`opstool-alerting.bicep`](../../dev-infrastructure/templates/opstool-alerting.bicep) and [`pagerduty-actiongroup.bicep`](../../dev-infrastructure/modules/metrics/pagerduty-actiongroup.bicep) |
| PagerDuty objects and Slack connection | PagerDuty and Slack administration UIs |

Do not duplicate evolving alert, collector, or metric catalogs in this document.
