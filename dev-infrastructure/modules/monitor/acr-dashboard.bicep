@description('Name for the Azure portal dashboard resource')
param dashboardName string

@description('Azure region for the dashboard (dashboards are typically deployed to "global" location alongside the shared ACR)')
param location string

@description('Display title shown on the dashboard tile')
param dashboardTitle string

@description('Resource ID of the Log Analytics workspace the dashboard queries')
param logAnalyticsWorkspaceId string

var workspaceDisplayName = last(split(logAnalyticsWorkspaceId, '/'))

// General overview: request volume, push/pull activity, top repositories,
// result codes, latency. Sourced from the diagnostic logs
// (ContainerRegistryLoginEvents/ContainerRegistryRepositoryEvents) and the
// ACR platform metrics (AzureMetrics) - see diagnostic-settings.bicep. Azure
// Monitor does not expose a replication-specific metric or log for ACR (only
// Total/SuccessfulPull/PushCount and StorageUsed); replication health must
// still be checked via `az acr replication list`.
var overviewCharts = [
  {
    title: 'Total Requests Over Time (Login + Repository, last 24h)'
    query: 'union ContainerRegistryLoginEvents, ContainerRegistryRepositoryEvents\n| where TimeGenerated > ago(24h)\n| summarize Requests=count() by bin(TimeGenerated, 10m)\n| render timechart'
    controlType: 'FrameControlChart'
    specificChart: 'Line'
    dimensions: {
      aggregation: 'Sum'
      splitBy: []
      xAxis: { name: 'TimeGenerated', type: 'datetime' }
      yAxis: [{ name: 'Requests', type: 'long' }]
    }
  }
  {
    title: 'Push vs Pull Count (ACR Metrics, last 24h)'
    query: 'AzureMetrics\n| where TimeGenerated > ago(24h)\n| where MetricName in ("TotalPullCount", "TotalPushCount")\n| summarize Count=sum(todouble(Total)) by bin(TimeGenerated, 10m), MetricName\n| render timechart'
    controlType: 'FrameControlChart'
    specificChart: 'Line'
    dimensions: {
      aggregation: 'Sum'
      splitBy: [{ name: 'MetricName', type: 'string' }]
      xAxis: { name: 'TimeGenerated', type: 'datetime' }
      yAxis: [{ name: 'Count', type: 'real' }]
    }
  }
  {
    title: 'Login Result Breakdown (last 24h)'
    query: 'ContainerRegistryLoginEvents\n| where TimeGenerated > ago(24h)\n| summarize count() by bin(TimeGenerated, 10m), ResultDescription\n| render timechart'
    controlType: 'FrameControlChart'
    specificChart: 'Line'
    dimensions: {
      aggregation: 'Sum'
      splitBy: [{ name: 'ResultDescription', type: 'string' }]
      xAxis: { name: 'TimeGenerated', type: 'datetime' }
      yAxis: [{ name: 'count_', type: 'long' }]
    }
  }
  {
    title: 'Average Request Duration (ms, last 24h)'
    query: 'union withsource=SourceTable ContainerRegistryLoginEvents, ContainerRegistryRepositoryEvents\n| where TimeGenerated > ago(24h)\n| summarize AvgDurationMs=avg(todouble(DurationMs)) by bin(TimeGenerated, 10m), SourceTable\n| render timechart'
    controlType: 'FrameControlChart'
    specificChart: 'Line'
    dimensions: {
      aggregation: 'Average'
      splitBy: [{ name: 'SourceTable', type: 'string' }]
      xAxis: { name: 'TimeGenerated', type: 'datetime' }
      yAxis: [{ name: 'AvgDurationMs', type: 'real' }]
    }
  }
]

// Rendered as a table (not a bar chart) - repository names are long and a
// categorical bar chart with 10 long labels was unreadable.
var topRepositoriesChart = {
  title: 'Top Repositories by Pull Count (last 24h)'
  query: 'ContainerRegistryRepositoryEvents\n| where TimeGenerated > ago(24h)\n| where OperationName == "Pull"\n| summarize Pulls=count() by Repository\n| top 10 by Pulls desc'
  controlType: 'AnalyticsGrid'
}

// Throttling: 429s specifically, split out so on-call can jump straight to
// the signal that matters during an incident without wading through general
// traffic charts. Query columns use the raw diagnostic log schema, in
// particular `ResultDescription` (a string, e.g. "200"/"429"), not
// `HttpStatusCode`.
var throttlingCharts = [
  {
    title: 'ACR 429 Throttling (Login Events, last 24h)'
    query: 'ContainerRegistryLoginEvents\n| where TimeGenerated > ago(24h)\n| where ResultDescription == "429"\n| summarize Throttled429=count() by bin(TimeGenerated, 10m)\n| render timechart'
    controlType: 'FrameControlChart'
    specificChart: 'Line'
    dimensions: {
      aggregation: 'Sum'
      splitBy: []
      xAxis: { name: 'TimeGenerated', type: 'datetime' }
      yAxis: [{ name: 'Throttled429', type: 'long' }]
    }
  }
  {
    title: 'Total vs Throttled Login Requests (last 24h)'
    query: 'ContainerRegistryLoginEvents\n| where TimeGenerated > ago(24h)\n| summarize Total=count(), Throttled429=countif(ResultDescription == "429") by bin(TimeGenerated, 10m)\n| extend ThrottleRatePct = round(100.0 * Throttled429 / Total, 2)\n| project TimeGenerated, Total, Throttled429, ThrottleRatePct\n| render timechart'
    controlType: 'FrameControlChart'
    specificChart: 'Line'
    dimensions: {
      aggregation: 'Sum'
      splitBy: []
      xAxis: { name: 'TimeGenerated', type: 'datetime' }
      yAxis: [
        { name: 'Total', type: 'long' }
        { name: 'Throttled429', type: 'long' }
      ]
    }
  }
]

// Builds one Microsoft_OperationsManagementSuite_Workspace/LogsDashboardPart
// tile for a chart or table definition (see overviewCharts/throttlingCharts/
// topRepositoriesChart above). `chart.controlType` is 'FrameControlChart' for
// a line/bar chart (requires specificChart + dimensions) or 'AnalyticsGrid'
// for a plain table (specificChart/dimensions unused). Bicep user-defined
// functions cannot read outer-scope params/vars, so the workspace resource ID
// and display name are passed in explicitly.
func buildChartPart(position object, chart object, workspaceId string, workspaceLabel string) object => {
  position: position
  metadata: {
    inputs: [
      { name: 'resourceTypeMode', value: 'workspace', isOptional: true }
      { name: 'ComponentId', value: workspaceId, isOptional: true }
      { name: 'Scope', value: { resourceIds: [workspaceId] }, isOptional: true }
      { name: 'PartId', isOptional: true }
      { name: 'Version', value: '2.0', isOptional: true }
      { name: 'TimeRange', value: 'P1D', isOptional: true }
      { name: 'DashboardId', isOptional: true }
      { name: 'DraftRequestParameters', isOptional: true }
      { name: 'Query', value: chart.query, isOptional: true }
      { name: 'ControlType', value: chart.controlType, isOptional: true }
      { name: 'SpecificChart', value: chart.?specificChart, isOptional: true }
      { name: 'PartTitle', value: chart.title, isOptional: true }
      { name: 'PartSubTitle', value: workspaceLabel, isOptional: true }
      { name: 'Dimensions', value: chart.?dimensions, isOptional: true }
      { name: 'LegendOptions', value: { isEnabled: true, position: 'Bottom' }, isOptional: true }
      { name: 'IsQueryContainTimeRange', value: false, isOptional: true }
    ]
    type: 'Extension/Microsoft_OperationsManagementSuite_Workspace/PartType/LogsDashboardPart'
    settings: {
      content: {
        Query: chart.query
        ControlType: chart.controlType
        SpecificChart: chart.?specificChart
        PartTitle: chart.title
        PartSubTitle: workspaceLabel
      }
    }
  }
}

func buildMarkdownPart(position object, title string, content string) object => {
  position: position
  metadata: {
    inputs: []
    type: 'Extension/HubsExtension/PartType/MarkdownPart'
    settings: {
      content: {
        settings: {
          content: content
          title: title
          subtitle: ''
          markdownSource: 1
        }
      }
    }
  }
}

resource dashboard 'Microsoft.Portal/dashboards@2022-12-01-preview' = {
  name: dashboardName
  location: location
  tags: {
    'hidden-title': dashboardTitle
  }
  properties: {
    lenses: [
      {
        order: 0
        parts: concat(
          [
            buildMarkdownPart(
              { x: 0, y: 0, colSpan: 16, rowSpan: 2 },
              dashboardTitle,
              '## ${dashboardTitle}\n\nMetrics and logs from the `${workspaceDisplayName}` Log Analytics workspace.'
            )
            buildMarkdownPart({ x: 0, y: 2, colSpan: 16, rowSpan: 1 }, 'Overview', '### Overview')
          ],
          [
            buildChartPart({ x: 0, y: 3, colSpan: 8, rowSpan: 4 }, overviewCharts[0], logAnalyticsWorkspaceId, workspaceDisplayName)
            buildChartPart({ x: 8, y: 3, colSpan: 8, rowSpan: 4 }, overviewCharts[1], logAnalyticsWorkspaceId, workspaceDisplayName)
            buildChartPart({ x: 0, y: 7, colSpan: 8, rowSpan: 4 }, overviewCharts[2], logAnalyticsWorkspaceId, workspaceDisplayName)
            buildChartPart({ x: 8, y: 7, colSpan: 8, rowSpan: 4 }, topRepositoriesChart, logAnalyticsWorkspaceId, workspaceDisplayName)
            buildChartPart({ x: 0, y: 11, colSpan: 16, rowSpan: 4 }, overviewCharts[3], logAnalyticsWorkspaceId, workspaceDisplayName)
          ],
          [
            buildMarkdownPart({ x: 0, y: 15, colSpan: 16, rowSpan: 1 }, 'Throttling', '### Throttling')
          ],
          [
            buildChartPart({ x: 0, y: 16, colSpan: 8, rowSpan: 4 }, throttlingCharts[0], logAnalyticsWorkspaceId, workspaceDisplayName)
            buildChartPart({ x: 8, y: 16, colSpan: 8, rowSpan: 4 }, throttlingCharts[1], logAnalyticsWorkspaceId, workspaceDisplayName)
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
