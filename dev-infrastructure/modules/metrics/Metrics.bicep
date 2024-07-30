// This template is copied from https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/ARO-Pipelines?path=/metrics/infra/Templates/Metrics.bicep
// Ideally this template is consumed from ACR.

// api-version=2021-06-01-preview is the internal Microsoft API, and api-version=2021-06-03-preview is for external customer use.
// The internal API version enables additional configurations options, including Geneva Metrics (MDM) stamp selection, and Geneva Metrics (MDM) ingestion configuration.
// Internal Microsoft customers should use the internal API to be able to link their existing Geneva Metrics (MDM) accounts, or to create managed Geneva Metrics (MDM) accounts on the appropriate stamp.
// https://msazure.visualstudio.com/One/_git/EngSys-MDA-AMCS?path=%2FSpecs%2FApiSchemas%2FOA%2F2021-06-01-preview%2FmonitoringAccounts_API.json&version=GBmaster&_a=contents
resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' = {
  name: 'aro-hcp-monitor'
  location: resourceGroup().location
}

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' = {
  name: 'aro-hcp-grafana'
  location: resourceGroup().location
  sku: {
    name: 'Standard'
  }
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
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
