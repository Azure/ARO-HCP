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

@description('Resource ID of the Kusto cluster (empty string when Kusto is disabled)')
param kustoClusterId string

@description('Region of the Kusto cluster (empty string when Kusto is disabled)')
param kustoRegion string

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
  }
}

module hcpAlerts '../modules/metrics/hcp-rules.bicep' = {
  name: 'hcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: sreActionGroups
  }
}

module rpAlerts '../modules/metrics/rp-rules.bicep' = {
  name: 'rpAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: rpActionGroups
  }
}

module msftAlerts '../modules/metrics/msft-rules.bicep' = {
  name: 'msftAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: msftActionGroups
  }
}

module svcIngestionAlerts '../modules/metrics/amw-ingestion-alerts.bicep' = {
  name: 'svcIngestionAlerts'
  params: {
    azureMonitorWorkspaceId: azureMonitoringWorkspaceId
    workspaceLabel: 'svc'
    actionGroups: sreActionGroups
    enabled: alertsEnabled
  }
}

module hcpIngestionAlerts '../modules/metrics/amw-ingestion-alerts.bicep' = {
  name: 'hcpIngestionAlerts'
  params: {
    azureMonitorWorkspaceId: hcpAzureMonitoringWorkspaceId
    workspaceLabel: 'hcp'
    actionGroups: sreActionGroups
    enabled: alertsEnabled
  }
}

// Since we deploy Kusto only once per Geography, only deploy alerts if the Kusto cluster is in the same region as the current resource group.
module kustoAlerts '../modules/metrics/kusto-alerts.bicep' = if (kustoClusterId != '' && kustoRegion == resourceGroup().location) {
  name: 'kustoAlerts'
  params: {
    kustoClusterId: kustoClusterId
    actionGroups: sreActionGroups
    enabled: alertsEnabled
  }
}
