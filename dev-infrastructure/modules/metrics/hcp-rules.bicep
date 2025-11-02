// Alerts and Recording Rules that apply to all services and svc/mgmt clusters.
// Excludes Hosted Control Plane Alerts and Recording Rules.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

module generatedAlerts 'rules/generatedHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
  }
}

module generatedRecordingRules 'rules/generatedHCPRecordingRules.bicep' = {
  name: 'generatedHCPRecordingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}
