@description('Azure Front Door Profile name')
param frontDoorProfileName string

@description('Azure Monitor Workspace ID for metrics export')
param azureMonitorWorkspaceId string

// Export AFD metrics to Azure Monitor Workspace for Grafana
resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' existing = {
  name: frontDoorProfileName
}

resource frontDoorDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = {
  name: 'afd-metrics-export'
  scope: frontDoorProfile
  properties: {
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
        retentionPolicy: {
          enabled: false
          days: 0
        }
      }
    ]
    workspaceId: azureMonitorWorkspaceId
  }
}