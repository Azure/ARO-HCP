@description('Name of the Azure Monitor Workspace')
param azureMonitorWorkspaceName string

@description('Maximum active time series limit (10M for 100 HCPs, max 20M via API)')
@minValue(1000000)
@maxValue(20000000)
param maxActiveTimeSeries int = 10000000

@description('Maximum events per minute limit (10M for 100 HCPs, max 20M via API)')
@minValue(1000000)
@maxValue(20000000)
param maxEventsPerMinute int = 10000000

// Existing Azure Monitor Workspace (parent resource for metrics container)
resource azureMonitorWorkspace 'Microsoft.Monitor/accounts@2023-04-03' existing = {
  name: azureMonitorWorkspaceName
}

// Update ingestion limits for the Azure Monitor Workspace
// Note: This must be deployed AFTER the workspace is created
// For limits > 20M, a support ticket is required
resource metricsContainer 'Microsoft.Monitor/accounts/metricsContainers@2025-05-03-preview' = {
  name: 'default'
  parent: azureMonitorWorkspace
  properties: {
    limits: {
      maxActiveTimeSeries: maxActiveTimeSeries
      maxEventsPerMinute: maxEventsPerMinute
    }
  }
}

output maxActiveTimeSeries int = maxActiveTimeSeries
output maxEventsPerMinute int = maxEventsPerMinute
