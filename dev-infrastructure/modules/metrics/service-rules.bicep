// Alerts and Recording Rules that apply to all services and svc/mgmt clusters.
// Excludes Hosted Control Plane Alerts and Recording Rules.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

module prometheus 'rules/prometheusAlertingRules.bicep' = {
  name: 'prometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}

module generatedAlerts 'rules/generatedPrometheusAlertingRules.bicep' = {
  name: 'generatedPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
  }
}

module defaultRuleGroups 'rules/defaultRecordingRuleGroups.bicep' = {
  name: 'defaultRecordingRuleGroups'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}

module generatedRecordingRules 'rules/generatedRecordingRules.bicep' = {
  name: 'generatedRecordingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}
