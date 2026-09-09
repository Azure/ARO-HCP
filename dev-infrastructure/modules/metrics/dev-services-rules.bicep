// Alerts that apply to services and route to the DEV action group.

@description('The Azure resource ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

module generatedAlerts 'rules/generatedDevServicesPrometheusAlertingRules.bicep' = {
  name: 'generatedDevServicesPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}
