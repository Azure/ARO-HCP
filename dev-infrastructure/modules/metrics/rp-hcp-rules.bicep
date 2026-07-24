// Alerts for HCP KubeAPIServer availability, routed to the RP ICM queue.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for hosted control planes)')
param azureMonitoringWorkspaceId string

param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

module generatedAlerts 'rules/generatedRPHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedRPHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}
