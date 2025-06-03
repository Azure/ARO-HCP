// Alerts and Recording Rules that apply to all services and svc/mgmt clusters.
// Excludes Hosted Control Plane Alerts and Recording Rules.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param allSev1ActionGroups array

param allSev2ActionGroups array

param allSev3ActionGroups array

param allSev4ActionGroups array

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
    allSev1ActionGroups: allSev1ActionGroups
    allSev2ActionGroups: allSev2ActionGroups
    allSev3ActionGroups: allSev3ActionGroups
    allSev4ActionGroups: allSev4ActionGroups
  }
}

module defaultRuleGroups 'rules/defaultRecordingRuleGroups.bicep' = {
  name: 'defaultRecordingRuleGroups'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}
