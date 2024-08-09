// This template is copied from https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/ARO-Pipelines?path=/metrics/infra/Templates/Metrics.bicep
// Ideally this template is consumed from ACR.

param grafanaName string

resource monitor 'Microsoft.Monitor/accounts@2023-04-03' = {
  name: 'aro-hcp-monitor'
  location: resourceGroup().location
  properties: {
    publicNetworkAccess: 'Enabled'
  }
}

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' = {
  name: grafanaName
  location: resourceGroup().location
  sku: {
    name: 'Standard'
  }
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    grafanaMajorVersion: '10'
    grafanaIntegrations: {
      azureMonitorWorkspaceIntegrations: [
        {
          azureMonitorWorkspaceResourceId: monitor.id
        }
      ]
    }
  }
}

// Assign the Monitoring Data Reader role to the Azure Managed Grafana system-assigned managed identity at the workspace scope
var dataReader = 'b0d8363b-8ddd-447d-831f-62ca05bff136'

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(monitor.id, grafana.id, dataReader)
  scope: monitor
  properties: {
    principalId: grafana.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

module alerts 'Alerts.bicep' = {
  name: 'alerts'
  params: {
    azureMonitoring: monitor.id
  }
}
