@description('Name of the existing ACR to configure diagnostic settings for')
param acrName string

@description('Resource ID of the Log Analytics workspace to send diagnostic logs to')
param logAnalyticsWorkspaceId string

@description('Name for the diagnostic settings resource')
param diagnosticSettingsName string = 'acr-diagnostic-logs'

resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

// Sends ACR repository and login events (including 429 throttling events) to
// Log Analytics so pull/push throughput issues can be diagnosed directly,
// instead of inferring them from downstream Kubernetes events.
resource acrDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = {
  scope: acr
  name: diagnosticSettingsName
  properties: {
    workspaceId: logAnalyticsWorkspaceId
    logs: [
      {
        category: 'ContainerRegistryRepositoryEvents'
        enabled: true
      }
      {
        category: 'ContainerRegistryLoginEvents'
        enabled: true
      }
    ]
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
      }
    ]
  }
}
