@description('ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

@description('ID of the Azure Monitor Workspace for hosted control planes')
param hcpAzureMonitoringWorkspaceId string

@description('The ICM environment')
param icmEnvironment string

@description('ICM connection Name')
param icmConnectionName string

@description('ICM connection id')
param icmConnectionId string

@description('Name of the ICM Action Group')
param icmActionGroupNameSRE string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameSRE string

@description('ICM routing ID')
param icmRoutingIdSRE string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSRE string

@description('Name of the ICM Action Group')
param icmActionGroupNameSL string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameSL string

@description('ICM routing ID')
param icmRoutingIdSL string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSL string

module actionGroups '../modules/metrics/actiongroups.bicep' = {
  name: 'actionGroups'
  params: {
    icmEnvironment: icmEnvironment
    icmConnectionName: icmConnectionName
    icmConnectionId: icmConnectionId
    icmActionGroupNameSRE: icmActionGroupNameSRE
    icmActionGroupShortNameSRE: icmActionGroupShortNameSRE
    icmRoutingIdSRE: icmRoutingIdSRE
    icmAutomitigationEnabledSRE: icmAutomitigationEnabledSRE
    icmActionGroupNameSL: icmActionGroupNameSL
    icmActionGroupShortNameSL: icmActionGroupShortNameSL
    icmRoutingIdSL: icmRoutingIdSL
    icmAutomitigationEnabledSL: icmAutomitigationEnabledSL
  }
}

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: [actionGroups.outputs.actionGroupsSL]
  }
}

module hcpAlerts '../modules/metrics/hcp-rules.bicep' = {
  name: 'hcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: [actionGroups.outputs.actionGroupsSRE]
  }
}
