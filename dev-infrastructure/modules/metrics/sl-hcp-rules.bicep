// SL alerts that apply to HCP clusters and route to the SL action group.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for HCPs)')
param azureMonitoringWorkspaceId string

param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

module generatedAlerts 'rules/generatedSLHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedSLHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}
