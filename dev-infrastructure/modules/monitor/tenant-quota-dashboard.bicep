@description('Name for the Azure portal dashboard resource')
param dashboardName string

@description('Azure region for the dashboard resource')
param location string

@description('Environment label shown in the dashboard title and section headers (e.g. Dev, Int, Stg, Prod)')
param envLabel string

@description('Resource ID of the Azure Monitor Workspace (managed Prometheus) that every panel queries')
param azureMonitorWorkspaceId string

@description('Subscriptions charted by this dashboard: objects with id and name. Regions are shared by every subscription in this list.')
param subscriptions array

@description('Azure regions to chart compute/network quota for, shared by every subscription passed in "subscriptions"')
param regions array

@description('tenant_name label value for the directory (Azure AD object) quota panel')
param tenantName string

// One Azure Monitor Workspace (opstool-monitor-usw3, DEV-only) serves every
// environment's dashboard: the tenant-quota-collector runs once, in DEV, and
// remotely collects quota for every subscription across every environment
// (see tooling/tenant-quota/pkg/subscriptionquota). Dashboards for INT/STG/
// PROD therefore still query this single DEV workspace, just scoped to a
// different subscription_id/region set.
var subscriptionIdPattern = join(map(subscriptions, sub => sub.id), '|')

// ceil(N/2): number of two-panel rows needed to render N per-region panels.
var computeRows = (length(regions) + 1) / 2
var networkRows = computeRows

var titleRowY = 0
var computeHeaderY = titleRowY + 3
var computeRowsY = computeHeaderY + 1
var networkHeaderY = computeRowsY + (4 * computeRows)
var networkRowsY = networkHeaderY + 1
var rbacHeaderY = networkRowsY + (4 * networkRows)
var rbacRowY = rbacHeaderY + 2
var directoryHeaderY = rbacRowY + 4
var directoryRowY = directoryHeaderY + 2
var e2eHeaderY = directoryRowY + 4
var e2eRowY = e2eHeaderY + 2

// For-expressions can only be assigned directly to a variable/output/resource
// property, not passed inline as a concat() argument, so the per-region
// panel rows are built here and referenced by name below.
var computePanels = [
  for i in range(0, length(regions)): buildMonitorChartPart(
    { x: (i % 2) * 8, y: computeRowsY + ((i / 2) * 4), colSpan: 8, rowSpan: 4 },
    'Compute Quota — ${regions[i]}',
    [computeQuery(regions[i], subscriptionIdPattern)],
    azureMonitorWorkspaceId
  )
]
var networkPanels = [
  for i in range(0, length(regions)): buildMonitorChartPart(
    { x: (i % 2) * 8, y: networkRowsY + ((i / 2) * 4), colSpan: 8, rowSpan: 4 },
    'Network Quota — ${regions[i]}',
    [networkQuery(regions[i], subscriptionIdPattern)],
    azureMonitorWorkspaceId
  )
]

func computeQuery(region string, subscriptionIdPattern string) string =>
  'sum(azure_quota_usage{source="compute",region="${region}",subscription_id=~"${subscriptionIdPattern}"}) by (subscription_name, localized_name) / sum(azure_quota_limit{source="compute",region="${region}",subscription_id=~"${subscriptionIdPattern}"}) by (subscription_name, localized_name) * 100'

// Network watchers are excluded: they are a per-region singleton quota that
// is always 1/1 (100%) and drowns out the quotas operators actually act on
// (see the same exclusion in tooling/tenant-quota/alerting.bicep).
func networkQuery(region string, subscriptionIdPattern string) string =>
  'sum(azure_quota_usage{source="network",region="${region}",subscription_id=~"${subscriptionIdPattern}",localized_name!~"(?i)^network watchers$"}) by (subscription_name, localized_name) / sum(azure_quota_limit{source="network",region="${region}",subscription_id=~"${subscriptionIdPattern}",localized_name!~"(?i)^network watchers$"}) by (subscription_name, localized_name) * 100'

// Role-assignment quota is subscription-scoped, not regional (see the "rbac"
// source in tooling/tenant-quota/pkg/subscriptionquota), so this is a single
// panel rather than one per region.
func rbacQuery(subscriptionIdPattern string) string =>
  'sum(azure_quota_usage{source="rbac",subscription_id=~"${subscriptionIdPattern}"}) by (subscription_name) / sum(azure_quota_limit{source="rbac",subscription_id=~"${subscriptionIdPattern}"}) by (subscription_name) * 100'

// Uses max by (not sum by) to collapse duplicate series from Azure Managed Prometheus HA replica
// pairs without double-counting the value, matching the dedup pattern used in alerting.bicep.
func directoryQuery(tenantName string) string => 'max by (tenant_name) (tenant_quota_usage_percentage{tenant_name="${tenantName}"})'

// Hours elapsed since each E2E resource group's deleteAfter TTL tag expired.
// Subscriptions in this dashboard that are not E2E subscriptions (e.g. the
// shared infra subscriptions) simply have no matching series and render no
// lines.
// max without (prometheus_replica) collapses Azure Managed Prometheus HA replica
// duplicates (which report identical values) into a single series per resource group.
func e2eExpiryQuery(subscriptionIdPattern string) string =>
  '(time() - max without (prometheus_replica) (e2e_resource_group_expiry_timestamp{subscription_id=~"${subscriptionIdPattern}"})) / 3600'

// Builds one Extension/HubsExtension/PartType/MonitorChartPart tile querying
// the Azure Monitor Workspace with PromQL. `queries` is one or more PromQL
// expressions rendered as separate lines/series on the same chart. Bicep
// user-defined functions cannot read outer-scope params/vars, so every value
// the tile needs is passed in explicitly.
// map() is used instead of a for-expression here because for-expressions are
// only valid as values of resource/module/variable/output declarations, not
// as arbitrary properties inside a function's returned object literal.
func buildMetric(q string, workspaceId string) object => {
  metricVisualization: {}
  name: q
  namespace: 'prometheus metrics'
  query: { queryContent: q, queryType: 'PromQL' }
  resourceMetadata: { id: workspaceId }
}

func buildChartOptions(title string, queries array, workspaceId string) object => {
  chart: {
    grouping: { dimension: 'Microsoft.ResourceId', top: 100 }
    metrics: map(queries, q => buildMetric(q, workspaceId))
    timespan: {
      grain: 1
      relative: { duration: 86400000 }
      showUTCTime: true
    }
    title: title
    titleKind: 2
    visualization: {
      axisVisualization: {
        x: { axisType: 2, isVisible: true }
        y: { axisType: 1, isVisible: true }
      }
      chartType: 2
      legendVisualization: { hideHoverCard: false, hideLabelNames: true, isVisible: true, position: 2 }
    }
  }
}

func buildMonitorChartPart(position object, title string, queries array, workspaceId string) object => {
  position: position
  metadata: {
    inputs: [
      { isOptional: true, name: 'sharedTimeRange' }
      { isOptional: true, name: 'options', value: buildChartOptions(title, queries, workspaceId) }
    ]
    settings: {
      content: {
        options: buildChartOptions(title, queries, workspaceId)
      }
    }
    type: 'Extension/HubsExtension/PartType/MonitorChartPart'
  }
}

func buildMarkdownPart(position object, title string, content string) object => {
  position: position
  metadata: {
    inputs: []
    type: 'Extension/HubsExtension/PartType/MarkdownPart'
    settings: {
      content: {
        settings: { content: content, title: title, subtitle: '', markdownSource: 1 }
      }
    }
  }
}

resource dashboard 'Microsoft.Portal/dashboards@2022-12-01-preview' = {
  name: dashboardName
  location: location
  tags: {
    'hidden-title': 'DEV CI Quota Monitoring — ${envLabel}'
  }
  properties: {
    lenses: [
      {
        order: 0
        parts: concat(
          [
            buildMarkdownPart(
              { x: 0, y: titleRowY, colSpan: 16, rowSpan: 3 },
              'DEV CI Quota Monitoring — ${envLabel}',
              'Compute, network, role-assignment, and directory quota, plus E2E resource-group expiry, from the tenant-quota-collector via the opstool Azure Monitor Workspace.'
            )
            buildMarkdownPart({ x: 0, y: computeHeaderY, colSpan: 16, rowSpan: 1 }, 'Compute Quota', '')
          ],
          computePanels,
          [
            buildMarkdownPart({ x: 0, y: networkHeaderY, colSpan: 16, rowSpan: 1 }, 'Network Quota', '')
          ],
          networkPanels,
          [
            buildMarkdownPart(
              { x: 0, y: rbacHeaderY, colSpan: 16, rowSpan: 2 },
              'Role-Assignment (RBAC) Quota',
              'Non-regional: role-assignment limits are subscription-wide.'
            )
            buildMonitorChartPart(
              { x: 0, y: rbacRowY, colSpan: 16, rowSpan: 4 },
              'RBAC Quota — ${envLabel}',
              [rbacQuery(subscriptionIdPattern)],
              azureMonitorWorkspaceId
            )
            buildMarkdownPart(
              { x: 0, y: directoryHeaderY, colSpan: 16, rowSpan: 2 },
              'Directory (Azure AD Object) Quota',
              'Tenant-wide: shared by every environment in this tenant.'
            )
            buildMonitorChartPart(
              { x: 0, y: directoryRowY, colSpan: 16, rowSpan: 4 },
              'Directory Quota — ${tenantName}',
              [directoryQuery(tenantName)],
              azureMonitorWorkspaceId
            )
            buildMarkdownPart(
              { x: 0, y: e2eHeaderY, colSpan: 16, rowSpan: 2 },
              'E2E Resource Group Expiry',
              'Hours elapsed since each E2E resource group\'s `deleteAfter` TTL tag expired. Subscriptions in this dashboard without E2E resource groups render no lines.'
            )
            buildMonitorChartPart(
              { x: 0, y: e2eRowY, colSpan: 16, rowSpan: 4 },
              'E2E Resource Group Expiry (hours past deleteAfter) — ${envLabel}',
              [e2eExpiryQuery(subscriptionIdPattern)],
              azureMonitorWorkspaceId
            )
          ]
        )
      }
    ]
    metadata: {
      model: {
        timeRange: {
          value: { relative: { duration: 24, timeUnit: 1 } }
          type: 'MsPortalFx.Composition.Configuration.ValueTypes.TimeRange'
        }
        filterLocale: { value: 'en-us' }
        filters: {
          value: {
            MsPortalFx_TimeRange: {
              model: { format: 'utc', granularity: 'auto', relative: '24h' }
              displayCache: { name: 'UTC Time', value: 'Past 24 hours' }
            }
          }
        }
      }
    }
  }
}

output dashboardId string = dashboard.id
