// Alerts that apply to service clusters and route to the SRE action group.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

module generatedAlerts 'rules/generatedSREServicePrometheusAlertingRules.bicep' = {
  name: 'generatedSREServicePrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
  }
}
