// Recording rules that apply to Hosted Control Planes.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

module generatedRecordingRules 'rules/generatedHCPRecordingRules.bicep' = {
  name: 'generatedHCPRecordingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
  }
}
