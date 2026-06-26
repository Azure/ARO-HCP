// Alerts and Recording Rules that apply to all services and svc/mgmt clusters.
// Excludes Hosted Control Plane Alerts and Recording Rules.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

module generatedAlerts 'rules/generatedHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}

module generatedRecordingRules 'rules/generatedHCPRecordingRules.bicep' = {
  name: 'generatedHCPRecordingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}
