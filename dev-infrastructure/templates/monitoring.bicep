@description('ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

@description('ID of the Azure Monitor Workspace for hosted control planes')
param hcpAzureMonitoringWorkspaceId string

@description('List of emails for Dev Alerting')
param devAlertingEmails string

@description('Comma seperated list of action groups for Sev 1 alerts.')
param sev1ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 2 alerts.')
param sev2ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 3 alerts.')
param sev3ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 4 alerts.')
param sev4ActionGroupIDs string

module actionGroups '../modules/metrics/actiongroups.bicep' = {
  name: 'actionGroups'
  params: {
    devAlertingEmails: devAlertingEmails
    sev1ActionGroupIDs: sev1ActionGroupIDs
    sev2ActionGroupIDs: sev2ActionGroupIDs
    sev3ActionGroupIDs: sev3ActionGroupIDs
    sev4ActionGroupIDs: sev4ActionGroupIDs
  }
}

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    allSev1ActionGroups: actionGroups.outputs.allSev1ActionGroups
    allSev2ActionGroups: actionGroups.outputs.allSev2ActionGroups
    allSev3ActionGroups: actionGroups.outputs.allSev3ActionGroups
    allSev4ActionGroups: actionGroups.outputs.allSev4ActionGroups
  }
}

module hcpAlerts '../modules/metrics/hcp-rules.bicep' = {
  name: 'hcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    allSev1ActionGroups: actionGroups.outputs.allSev1ActionGroups
    allSev2ActionGroups: actionGroups.outputs.allSev2ActionGroups
    allSev3ActionGroups: actionGroups.outputs.allSev3ActionGroups
    allSev4ActionGroups: actionGroups.outputs.allSev4ActionGroups
  }
}
