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
param icmActionGroupShortNameSRE string

@description('ICM routing ID')
param icmRoutingIdSRE string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSRE string

@description('Name of the ICM Action Group')
param icmActionGroupNameSL string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameSL string

@description('ICM routing ID')
param icmRoutingIdSL string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSL string

@description('Name of the ICM Action Group')
param icmActionGroupNameRP string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameRP string

@description('ICM routing ID')
param icmRoutingIdRP string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledRP string

@description('Name of the ICM Action Group')
param icmActionGroupNameMSFT string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameMSFT string

@description('ICM routing ID')
param icmRoutingIdMSFT string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledMSFT string

@description('Enable creating ICM action groups')
param manageConnection bool

@description('Whether ICM alerting is enabled for this region')
param alertsEnabled bool

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param alertSeverityCeiling int = 0

module actionGroups '../modules/metrics/actiongroups.bicep' = if (manageConnection) {
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
    icmActionGroupNameRP: icmActionGroupNameRP
    icmActionGroupShortNameRP: icmActionGroupShortNameRP
    icmRoutingIdRP: icmRoutingIdRP
    icmAutomitigationEnabledRP: icmAutomitigationEnabledRP
    icmActionGroupNameMSFT: icmActionGroupNameMSFT
    icmActionGroupShortNameMSFT: icmActionGroupShortNameMSFT
    icmRoutingIdMSFT: icmRoutingIdMSFT
    icmAutomitigationEnabledMSFT: icmAutomitigationEnabledMSFT
    alertingEnabled: alertsEnabled
  }
}

var slActionGroups = manageConnection ? [actionGroups.outputs.actionGroupsSL] : []
var rpActionGroups = manageConnection ? [actionGroups.outputs.actionGroupsRP] : []
var sreActionGroups = manageConnection ? [actionGroups.outputs.actionGroupsSRE] : []
var msftActionGroups = manageConnection ? [actionGroups.outputs.actionGroupsMSFT] : []

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module svcKubeAlerts '../modules/metrics/svc-kube-rules.bicep' = {
  name: 'svcKubeAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module hcpAlerts '../modules/metrics/hcp-rules.bicep' = {
  name: 'hcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: sreActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module slHcpAlerts '../modules/metrics/sl-hcp-rules.bicep' = {
  name: 'slHcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module sreServiceAlerts '../modules/metrics/sre-service-rules.bicep' = {
  name: 'sreServiceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: sreActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module rpAlerts '../modules/metrics/rp-rules.bicep' = {
  name: 'rpAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: rpActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module msftAlerts '../modules/metrics/msft-rules.bicep' = {
  name: 'msftAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: msftActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module ingestionAlerts '../modules/metrics/amw-ingestion-alerts.bicep' = {
  name: 'ingestionAlerts'
  params: {
    actionGroups: slActionGroups
    enabled: alertsEnabled
    lowEventIngestionThreshold: 1
  }
}

module hcpIngestionAlerts '../modules/metrics/amw-ingestion-alerts.bicep' = {
  name: 'hcpIngestionAlerts'
  params: {
    azureMonitorWorkspaceId: hcpAzureMonitoringWorkspaceId
    workspaceLabel: 'hcp'
    actionGroups: slActionGroups
    enabled: alertsEnabled
    lowEventIngestionThreshold: 1
    workspaces: [
      {
        id: azureMonitoringWorkspaceId
        label: 'svc'
        lowEventIngestionThreshold: 1
      }
      {
        id: hcpAzureMonitoringWorkspaceId
        label: 'hcp'
        lowEventIngestionThreshold: 5
      }
    ]
  }
}

output actionGroupSL string = manageConnection ? actionGroups.outputs.actionGroupsSL : ''
