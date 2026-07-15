# CI Quota Monitoring

Several Azure quotas directly constrain CI parallelism and capacity. The `tenant-quota` tool continuously monitors these quotas and exposes them as Prometheus metrics, making it possible to detect approaching limits before they cause job failures.

## Why Quota Monitoring Matters For CI

- **Role assignments per subscription** cap E2E parallelism — each HCP cluster consumes 24-41 role assignments depending on the RBAC scope mode. See [Pool Sizing And Subscription Constraints](identity-leasing.md#pool-sizing-and-subscription-constraints) for the cost model and capacity formulas.
- **Directory quota per tenant** caps identity creation — when the limit is reached, no managed identities can be created until the monthly Azure purge cycle frees quota. See [Why Identity Leasing Exists](identity-leasing.md#why-identity-leasing-exists).
- **Compute quotas** (vCPUs per VM family per region) cap how many HCP clusters can run simultaneously.
- **Network quotas** (public IPs per region) cap cluster networking resources.

## What tenant-quota Monitors

The `tenant-quota` tool collects two categories of quota data:

### Subscription quota

For each configured subscription and region, the collector exports:

- `azure_quota_usage` / `azure_quota_limit` labeled with `source`, `subscription_name`, `region`, and `quota_name`

The current sources are:

| Source | What it tracks |
|---|---|
| `rbac` | Role assignment count vs the configured limit |
| `compute` | Azure Compute regional usage quotas (vCPUs per VM family) |
| `network` | Azure Network regional usage quotas (public IPs) |

### Tenant directory quota

For each configured tenant, the collector queries Microsoft Graph and exports:

- `tenant_quota_usage_percentage`, `tenant_quota_total`, `tenant_quota_used`, `tenant_remaining_capacity`

These are labeled with `tenant_id` and `tenant_name`.

## Where It Runs

The collector runs as a workload on the standalone `opstool` AKS cluster in `westus3`. Metrics flow through the shared `opstool` Prometheus stack into the `opstool` Azure Monitor Workspace.

For cluster architecture and deployment details, see [Opstool Cluster Guide](../ops/opstool-cluster-guide.md). For tool-specific deployment, configuration, and credential management, see [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md).

## Azure Dashboard

The quickest way to check current quota usage is the [Azure Quota Dashboard](https://portal.azure.com/#@redhat0.onmicrosoft.com/dashboard/arm/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourcegroups/dashboards/providers/microsoft.portal/dashboards/901b128a-124f-43e6-a797-5fcf3d1e83fe), a manually managed Azure portal dashboard that visualizes the exporter's data. It provides a real-time overview of quota usage across subscriptions without needing to query Prometheus directly.

## Alerts

Alert rules are defined in `tooling/tenant-quota/alerting.bicep` and deployed into the `opstool` Azure Monitor Workspace. Notifications go through the shared `opstool-email-alerts` Action Group. The primary alert fires when no metrics have been received for an extended period, which typically indicates a collector pod issue or an expired service principal credential.

## When Quota Is Tight

1. Check the [Azure Quota Dashboard](https://portal.azure.com/#@redhat0.onmicrosoft.com/dashboard/arm/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourcegroups/dashboards/providers/microsoft.portal/dashboards/901b128a-124f-43e6-a797-5fcf3d1e83fe) for current usage vs limits.
2. For role assignments, cross-reference with the capacity formulas in [Pool Sizing And Subscription Constraints](identity-leasing.md#pool-sizing-and-subscription-constraints) to understand how current usage maps to maximum concurrency.
3. If a quota increase is needed, request it through the Azure portal: **Subscriptions** > select the subscription > **Usage + quotas** > find the quota > request a new limit.

## See Also

- [CI Identity Leasing](identity-leasing.md) — role-assignment cost model and pool sizing
- [CI Overview](README.md)
- [Opstool Cluster Guide](../ops/opstool-cluster-guide.md)
- [`tooling/tenant-quota/README.md`](../../tooling/tenant-quota/README.md) — tool deployment, configuration, and troubleshooting
