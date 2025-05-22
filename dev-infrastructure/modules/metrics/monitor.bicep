@description('The grafana instance to integrate with')
param grafanaResourceId string

@description('Metrics region monitor name')
param monitorName string

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

import * as res from '../resource.bicep'

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' = {
  name: monitorName
  location: resourceGroup().location
}

module defaultRuleGroups 'rules/defaultRecordingRuleGroups.bicep' = {
  name: 'defaultRecordingRuleGroups'
  params: {
    azureMonitorWorkspaceLocation: resourceGroup().location
    azureMonitorWorkspaceName: monitor.name
    regionalResourceGroup: resourceGroup().name
  }
}

// Assign the Monitoring Data Reader role to the Azure Managed Grafana system-assigned managed identity at the workspace scope
var dataReader = 'b0d8363b-8ddd-447d-831f-62ca05bff136'

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(monitor.id, grafana.id, dataReader)
  scope: monitor
  properties: {
    principalId: grafana.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

module actionGroups 'actiongroups.bicep' = {
  name: 'actionGroups'
  params: {
    devAlertingEmails: devAlertingEmails
    sev1ActionGroupIDs: sev1ActionGroupIDs
    sev2ActionGroupIDs: sev2ActionGroupIDs
    sev3ActionGroupIDs: sev3ActionGroupIDs
    sev4ActionGroupIDs: sev4ActionGroupIDs
  }
}

module prometheus 'rules/prometheusAlertingRules.bicep' = {
  name: 'prometheusAlertingRules'
  params: {
    azureMonitoring: monitor.id
  }
}
module generatedAlerts 'rules/generatedPrometheusAlertingRules.bicep' = {
  name: 'generatedPrometheusAlertingRules'
  params: {
    azureMonitoring: monitor.id
    allSev1ActionGroups: actionGroups.outputs.allSev1ActionGroups
    allSev2ActionGroups: actionGroups.outputs.allSev2ActionGroups
    allSev3ActionGroups: actionGroups.outputs.allSev3ActionGroups
    allSev4ActionGroups: actionGroups.outputs.allSev4ActionGroups
  }
}

output monitorId string = monitor.id
output monitorPrometheusQueryEndpoint string = monitor.properties.metrics.prometheusQueryEndpoint
