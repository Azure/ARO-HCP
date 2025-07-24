@description('ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

@description('ID of the Azure Monitor Workspace for hosted control planes')
param hcpAzureMonitoringWorkspaceId string

@description('List of emails for Dev Alerting')
param devAlertingEmails string

@description('The ICM environment')
param icmEnvironment string

@description('Name of the ICM Action Group')
param icmActionGroupName string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortName string

@description('ICM routing ID')
param icmRoutingId string

@description('ICM connection Name')
param icmConnectionName string

@description('ICM connection id')
param icmConnectionId string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabled string

module actionGroups '../modules/metrics/actiongroups.bicep' = {
  name: 'actionGroups'
  params: {
    devAlertingEmails: devAlertingEmails
    icmEnvironment: icmEnvironment
    icmActionGroupName: icmActionGroupName
    icmActionGroupShortName: icmActionGroupShortName
    icmRoutingId: icmRoutingId
    icmConnectionName: icmConnectionName
    icmConnectionId: icmConnectionId
    icmAutomitigationEnabled: icmAutomitigationEnabled
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
