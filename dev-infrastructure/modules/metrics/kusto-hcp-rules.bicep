// Kusto-only alerts that apply to hosted control planes.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for HCPs)')
param azureMonitoringWorkspaceId string

@description('Action group resource IDs to notify when alerts fire')
param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

module generatedAlerts 'rules/generatedKustoOnlyHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedKustoOnlyHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}
