@description('Name of the Azure Monitor Workspace')
param azureMonitorWorkspaceName string

@description('Location of the Azure Monitor Workspace')
param location string

@description('Maximum active time series limit (2M initial, bump when hitting 50% utilization)')
param maxActiveTimeSeries int = 2000000

@description('Maximum events per minute limit (2M initial, bump when hitting 50% utilization)')
param maxEventsPerMinute int = 2000000

// Existing Azure Monitor Workspace (parent resource for metrics container)
resource azureMonitorWorkspace 'Microsoft.Monitor/accounts@2023-04-03' existing = {
  name: azureMonitorWorkspaceName
}

// Update ingestion limits for the Azure Monitor Workspace
// Note: This must be deployed AFTER the workspace is created
// For limits > 20M, a support ticket is required
// BCP187: location required by API but missing from Bicep type definition
// See: https://github.com/Azure/bicep-types-az/issues/2572
resource metricsContainer 'Microsoft.Monitor/accounts/metricsContainers@2025-10-03-preview' = {
  name: 'default'
  parent: azureMonitorWorkspace
  #disable-next-line BCP187
  location: location
  properties: {
    limits: {
      maxActiveTimeSeries: maxActiveTimeSeries
      maxEventsPerMinute: maxEventsPerMinute
    }
  }
}

output maxActiveTimeSeries int = maxActiveTimeSeries
output maxEventsPerMinute int = maxEventsPerMinute
