# AMW Metrics Usage Insights

The Region deployment exports the `MetricsUsageDetails` log category from both
regional `services-*` and `hcps-*` Azure Monitor workspaces (AMWs). This enables
[Metrics Usage Insights (preview)](https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/metrics-usage-insights)
for investigating metric cardinality, ingestion volume, and unused metrics.
It exports daily usage summaries, not raw Prometheus samples. It does not enable
`AllMetrics`, duplicate the raw metric stream, or change other diagnostic settings.

## Ownership and Lifecycle

One `amw-usage-<environment>-<region>` Log Analytics workspace (LAW) is owned by
the Region deployment, in the same resource group, subscription, tenant, and
region as the AMWs. Both AMWs send logs to its `AMWMetricsUsageDetails` table via
their own `metrics-usage-insights` diagnostic setting. The workspace uses the
existing LAW module with the `PerGB2018` SKU and 90-day retention.

The existing global ACR diagnostics and image-sync LAWs have separate purposes
and lifecycles; this does not reuse them or assume cross-tenant access. The
regional LAW survives individual service/management cluster and HCP deletion,
but not deletion of its regional resource group. It is not an archival backup.
Access follows the regional resource group's existing RBAC; no new role grants
are added.

## Rollout and Verification

`monitoring.metricsUsageInsights.enabled` defaults to false. It is enabled for
CSPR and public INT/STG/PROD, except PROD `eastus2euap`, which is not listed as a
supported preview region. CI (`ci00`/`ci01`), personal/performance environments,
and the retired integrated DEV environment remain disabled. Before onboarding
new regions, check the preview's supported-region list and override the flag to
false where necessary.

Deploy the Region infrastructure to create the LAW and both diagnostic settings.
CSPR receives changes through its normal pipeline. Public environments require
the corresponding `sdp-pipelines` update and controlled EV2 rollout; see
[ARO HCP pipelines](https://aka.ms/arohcp-pipelines). Start with INT, verify data
arrival, then promote through STG and PROD. The deployment identity must be able
to create the LAW and AMW diagnostic settings, and the subscription must have
`Microsoft.OperationalInsights` registered.

Allow approximately 24 hours after enabling the settings before checking
**Monitoring > Metrics usage insights** on each AMW or querying the LAW's
`AMWMetricsUsageDetails` table. Usage data is calculated daily and reflects the
previous day, not live utilization. Confirm records arrive for both AMW resource
IDs; an AMW with no metric ingestion may have no insights. Microsoft currently
documents no additional charges for this feature's usage data, queries, or
storage during preview; check current terms before rollout.

Setting the flag to false prevents deployment but does not remove existing
resources under incremental ARM deployments. To stop an already-enabled export,
explicitly remove only the two `metrics-usage-insights` settings through the
approved operational process. Retain the LAW for its history, or explicitly
delete it when decommissioning the region and no longer needing that data.
