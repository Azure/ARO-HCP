@description('ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

@description('ID of the Azure Monitor Workspace for hosted control planes')
param hcpAzureMonitoringWorkspaceId string

@description('List of emails for Dev Alerting')
param devAlertingEmails string


module actionGroups '../modules/metrics/actiongroups.bicep' = {
  name: 'actionGroups'
  params: {
    devAlertingEmails: devAlertingEmails
  }
}

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: actionGroups.outputs.actionGroups
  }
}

module hcpAlerts '../modules/metrics/hcp-rules.bicep' = {
  name: 'hcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: actionGroups.outputs.actionGroups
  }
}
