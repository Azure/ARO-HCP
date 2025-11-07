// Alerts and Recording Rules for Hosted Control Planes
// Split into SRE-routed and SL-routed alerts

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

@description('Action groups for SRE team alerts')
param sreActionGroups array

@description('Action groups for Service Lifecycle team alerts')
param slActionGroups array

module generatedSREAlerts 'rules/generatedHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: sreActionGroups
  }
}

module generatedSLAlerts 'rules/generatedHCPSLPrometheusAlertingRules.bicep' = {
  name: 'generatedHCPSLPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: slActionGroups
  }
}
