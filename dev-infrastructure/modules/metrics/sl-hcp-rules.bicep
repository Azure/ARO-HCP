// SL alerts that apply to HCP clusters and route to the SL action group.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for HCPs)')
param azureMonitoringWorkspaceId string

param actionGroups array

module generatedAlerts 'rules/generatedSLHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedSLHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
  }
}
