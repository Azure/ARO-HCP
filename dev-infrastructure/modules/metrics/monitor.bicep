@description('Metrics global resource group name')
param globalResourceGroup string

@description('Metrics global MSI name')
param msiName string

@description('Metrics global Grafana name')
param grafanaName string

@description('Metrics region monitor name')
param monitorName string = 'aro-hcp-monitor'

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' = {
  name: monitorName
  location: resourceGroup().location
}

module defaultRuleGroups 'rules/defaultRecordingRuleGroups.bicep' ={
  name: 'defaultRecordingRuleGroups'
  params: {
   azureMonitorWorkspaceLocation: resourceGroup().location
   azureMonitorWorkspaceName: monitorName
   regionalResourceGroup: resourceGroup().name
  }
}
// Assign the Monitoring Data Reader role to the Azure Managed Grafana system-assigned managed identity at the workspace scope
var dataReader = 'b0d8363b-8ddd-447d-831f-62ca05bff136'

resource msi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: msiName
  scope: resourceGroup(globalResourceGroup)
}

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaName
  scope: resourceGroup(globalResourceGroup)
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(monitor.id, grafana.id, dataReader)
  scope: monitor
  properties: {
    principalId: grafana.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

module prometheus 'rules/prometheusAlertingRules.bicep' = {
  name: 'prometheusAlertingRules'
  params: {
    azureMonitoring: monitor.id
  }
}

output msiId string = msi.id
output grafanaId string = grafana.id
output monitorId string = monitor.id