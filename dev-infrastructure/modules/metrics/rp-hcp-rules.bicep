// KAS availability burn-rate alerts routed to the RP ICM queue.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for hosted control planes)')
param azureMonitoringWorkspaceId string

param actionGroups array

module generatedAlerts 'rules/generatedRPHCPPrometheusAlertingRules.bicep' = {
  name: 'generatedRPHCPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
  }
}
