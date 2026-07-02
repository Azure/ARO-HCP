// Kubernetes control plane alerts scoped to the svc workspace.
// These are the same upstream Kubernetes alerts also deployed to the hcp workspace,
// duplicated here so that gather-observability can detect them on the svc workspace.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

module generatedAlerts 'rules/generatedSvcKubePrometheusAlertingRules.bicep' = {
  name: 'generatedSvcKubePrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}
